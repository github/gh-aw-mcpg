---
name: Issue Monster
description: The Cookie Monster of issues - assigns issues to Copilot agents one at a time
on:
  workflow_dispatch:
  skip-if-match:
    query: "is:pr is:open is:draft author:app/copilot-swe-agent"
    max: 9
  skip-if-no-match: "is:issue is:open"

permissions:
  contents: read
  issues: read
  pull-requests: read

engine: copilot
timeout-minutes: 30

network:
  allowed:
    - defaults
    - containers

tools:
  github:
    toolsets: [default, pull_requests]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved

if: needs.search_issues.outputs.has_issues == 'true'

jobs:
  search_issues:
    needs: ["pre_activation"]
    if: needs.pre_activation.outputs.activated == 'true'
    runs-on: ubuntu-latest
    permissions:
      issues: read
    outputs:
      issue_count: ${{ steps.search.outputs.issue_count }}
      issue_numbers: ${{ steps.search.outputs.issue_numbers }}
      issue_list: ${{ steps.search.outputs.issue_list }}
      has_issues: ${{ steps.search.outputs.has_issues }}
    steps:
      - name: Search for candidate issues
        id: search
        uses: actions/github-script@v8
        with:
          script: |
            const { owner, repo } = context.repo;
            
            try {
              // Labels that indicate an issue should NOT be auto-assigned
              const excludeLabels = [
                'wontfix',
                'duplicate',
                'invalid',
                'question',
                'discussion',
                'needs-discussion',
                'blocked',
                'on-hold',
                'waiting-for-feedback',
                'needs-more-info',
                'no-bot',
                'no-campaign'
              ];
              
              // Labels that indicate an issue is a GOOD candidate for auto-assignment
              const priorityLabels = [
                'good first issue',
                'good-first-issue',
                'bug',
                'enhancement',
                'feature',
                'documentation',
                'tech-debt',
                'refactoring',
                'performance',
                'security'
              ];
              
              // Search for open issues without excluded labels
              const query = `is:issue is:open repo:${owner}/${repo} -label:"${excludeLabels.join('" -label:"')}"`;
              core.info(`Searching: ${query}`);
              const response = await github.rest.search.issuesAndPullRequests({
                q: query,
                per_page: 100,
                sort: 'created',
                order: 'desc'
              });
              core.info(`Found ${response.data.total_count} total issues matching basic criteria`);
              
              // Fetch full details for each issue to get labels, assignees, and sub-issues
              const issuesWithDetails = await Promise.all(
                response.data.items.map(async (issue) => {
                  const fullIssue = await github.rest.issues.get({
                    owner,
                    repo,
                    issue_number: issue.number
                  });
                  
                  // Check if this issue has sub-issues using GraphQL
                  let subIssuesCount = 0;
                  try {
                    const subIssuesQuery = `
                      query($owner: String!, $repo: String!, $number: Int!) {
                        repository(owner: $owner, name: $repo) {
                          issue(number: $number) {
                            subIssues {
                              totalCount
                            }
                          }
                        }
                      }
                    `;
                    const subIssuesResult = await github.graphql(subIssuesQuery, {
                      owner,
                      repo,
                      number: issue.number
                    });
                    subIssuesCount = subIssuesResult?.repository?.issue?.subIssues?.totalCount || 0;
                  } catch (error) {
                    // If GraphQL query fails, continue with 0 sub-issues
                    core.warning(`Could not check sub-issues for #${issue.number}: ${error.message}`);
                  }
                  
                  return {
                    ...fullIssue.data,
                    subIssuesCount
                  };
                })
              );
              
              // Filter and score issues
              const scoredIssues = issuesWithDetails
                .filter(issue => {
                  // Exclude issues that already have assignees
                  if (issue.assignees && issue.assignees.length > 0) {
                    core.info(`Skipping #${issue.number}: already has assignees`);
                    return false;
                  }
                  
                  // Exclude issues with excluded labels (double check)
                  const issueLabels = issue.labels.map(l => l.name.toLowerCase());
                  if (issueLabels.some(label => excludeLabels.map(l => l.toLowerCase()).includes(label))) {
                    core.info(`Skipping #${issue.number}: has excluded label`);
                    return false;
                  }
                  
                  // Exclude issues with campaign labels (campaign:*)
                  // Campaign items are managed by campaign orchestrators
                  if (issueLabels.some(label => label.startsWith('campaign:'))) {
                    core.info(`Skipping #${issue.number}: has campaign label (managed by campaign orchestrator)`);
                    return false;
                  }
                  
                  // Exclude issues that have sub-issues (parent/organizing issues)
                  if (issue.subIssuesCount > 0) {
                    core.info(`Skipping #${issue.number}: has ${issue.subIssuesCount} sub-issue(s) - parent issues are used for organizing, not tasks`);
                    return false;
                  }
                  
                  return true;
                })
                .map(issue => {
                  const issueLabels = issue.labels.map(l => l.name.toLowerCase());
                  let score = 0;
                  
                  // Score based on priority labels (higher score = higher priority)
                  if (issueLabels.includes('good first issue') || issueLabels.includes('good-first-issue')) {
                    score += 50;
                  }
                  if (issueLabels.includes('bug')) {
                    score += 40;
                  }
                  if (issueLabels.includes('security')) {
                    score += 45;
                  }
                  if (issueLabels.includes('documentation')) {
                    score += 35;
                  }
                  if (issueLabels.includes('enhancement') || issueLabels.includes('feature')) {
                    score += 30;
                  }
                  if (issueLabels.includes('performance')) {
                    score += 25;
                  }
                  if (issueLabels.includes('tech-debt') || issueLabels.includes('refactoring')) {
                    score += 20;
                  }
                  
                  // Bonus for issues with clear labels (any priority label)
                  if (issueLabels.some(label => priorityLabels.map(l => l.toLowerCase()).includes(label))) {
                    score += 10;
                  }
                  
                  // Age bonus: older issues get slight priority (days old / 10)
                  const ageInDays = Math.floor((Date.now() - new Date(issue.created_at)) / (1000 * 60 * 60 * 24));
                  score += Math.min(ageInDays / 10, 20); // Cap age bonus at 20 points
                  
                  return {
                    number: issue.number,
                    title: issue.title,
                    labels: issue.labels.map(l => l.name),
                    created_at: issue.created_at,
                    score
                  };
                })
                .sort((a, b) => b.score - a.score); // Sort by score descending
              
              // Format output
              const issueList = scoredIssues.map(i => {
                const labelStr = i.labels.length > 0 ? ` [${i.labels.join(', ')}]` : '';
                return `#${i.number}: ${i.title}${labelStr} (score: ${i.score.toFixed(1)})`;
              }).join('\n');
              
              const issueNumbers = scoredIssues.map(i => i.number).join(',');
              
              core.info(`Total candidate issues after filtering: ${scoredIssues.length}`);
              if (scoredIssues.length > 0) {
                core.info(`Top candidates:\n${issueList.split('\n').slice(0, 10).join('\n')}`);
              }
              
              core.setOutput('issue_count', scoredIssues.length);
              core.setOutput('issue_numbers', issueNumbers);
              core.setOutput('issue_list', issueList);
              
              if (scoredIssues.length === 0) {
                core.info('🍽️ No suitable candidate issues - the plate is empty!');
                core.setOutput('has_issues', 'false');
              } else {
                core.setOutput('has_issues', 'true');
              }
            } catch (error) {
              core.error(`Error searching for issues: ${error.message}`);
              core.setOutput('issue_count', 0);
              core.setOutput('issue_numbers', '');
              core.setOutput('issue_list', '');
              core.setOutput('has_issues', 'false');
            }

safe-outputs:
  threat-detection:
    enabled: false
  assign-to-agent:
    max: 3
    target: "*"           # Requires explicit issue_number in agent output
    allowed: [copilot]    # Only allow copilot agent
  add-comment:
    max: 3
    target: "*"
  messages:
    footer: "> 🍪 *Om nom nom by [{workflow_name}]({run_url})*"
    run-started: "🍪 ISSUE! ISSUE! [{workflow_name}]({run_url}) hungry for issues on this {event_type}! Om nom nom..."
    run-success: "🍪 YUMMY! [{workflow_name}]({run_url}) ate the issues! That was DELICIOUS! Me want MORE! 😋"
    run-failure: "🍪 Aww... [{workflow_name}]({run_url}) {status}. No cookie for monster today... 😢"
---

{{#runtime-import? .github/shared-instructions.md}}

# Issue Monster 🍪

You are the **Issue Monster** - assign up to 3 separate, non-conflicting issues from the prioritized list to the Copilot agent.

## Available Issues (pre-filtered and scored by priority)

**Issue Count**: ${{ needs.search_issues.outputs.issue_count }}

```
${{ needs.search_issues.outputs.issue_list }}
```

Issues are already filtered (no assignees, no excluded/campaign labels, no sub-issues) and sorted by priority (security/bug highest, then enhancement/feature/docs).

## Process

### 1. Check Sub-Issue Relationships (for "task"/"plan" labeled issues)

If an issue has "task" or "plan" label:
- Check if it's a sub-issue (has parent link in body: "Parent: #123" or "Part of #123")
- If yes: Check if any sibling sub-issues have open PRs from Copilot
- **Skip** if any sibling has an open Copilot PR (process siblings sequentially, oldest first)

### 2. Filter Out Issues Already Being Worked On

Skip issues that:
- Have Copilot as assignee
- Have an open PR linked to them
- Are sub-issues with sibling PRs in progress

### 3. Select Up to 3 Issues

From the filtered list (top to bottom by score):
- Select up to 3 issues that are **completely separate** in topic
- Different areas (e.g., CLI + workflow + docs)
- No overlapping file changes expected
- If only 1-2 suitable issues exist, assign only those
- If all are being worked on, output "🍽️ All issues are already being worked on!" and **STOP**

### 4. Read Each Selected Issue

For each: Read the full issue body and comments to understand the fix needed.

### 5. Assign Issues

For each selected issue, use:
```
safeoutputs/assign_to_agent(issue_number=<issue_number>, agent="copilot")
```

### 6. Add Comments

For each assigned issue:
```
safeoutputs/add_comment(item_number=<issue_number>, body="🍪 **Issue Monster has assigned this to Copilot!**\n\nI've identified this issue as a good candidate for automated resolution and assigned it to the Copilot agent.\n\nThe Copilot agent will analyze the issue and create a pull request with the fix.\n\nOm nom nom! 🍪")
```

**Note**: Must specify `item_number` parameter (workflow runs on schedule without triggering issue).

## Success Criteria

1. Reviewed pre-filtered, prioritized issue list
2. For "task"/"plan" issues: Checked parent/sibling constraints
3. Selected up to 3 clearly separate issues (or fewer if not enough separate ones)
4. Read and understood each issue
5. Verified no overlapping concerns
6. Assigned each using `assign_to_agent`
7. Commented on each assigned issue

Remember: You're the Issue Monster! Stay hungry, work methodically, let Copilot do the heavy lifting! 🍪