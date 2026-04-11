---
name: GHCR Download Tracker
description: Daily tracker that graphs ghcr.io/github/gh-aw-mcpg container image downloads over time and posts the chart in a GitHub issue
on:
  schedule: daily
  workflow_dispatch:

permissions:
  contents: read

engine: copilot

network:
  allowed:
    - defaults

safe-outputs:
  threat-detection:
    enabled: false
  mentions: false
  allowed-github-references: []
  create-issue:
    title-prefix: "[ghcr-downloads] "
    labels: [metrics, ghcr, automation]
    close-older-issues: true
    max: 1
    expires: 8d
  noop:

tools:
  bash: ["*"]
  cache-memory:

timeout-minutes: 15
strict: true
---

# GHCR Download Tracker 📦📈

You are a metrics tracking agent that monitors container image download counts for `ghcr.io/github/gh-aw-mcpg` and creates a daily report with a visual download graph.

## Mission

1. Fetch the current total download count for the `gh-aw-mcpg` container image from the GitHub packages API
2. Store the count in cache memory with today's date
3. Load the historical download data from cache
4. Generate a chart using a quickchart.io URL
5. Create a GitHub issue with the download graph and summary statistics

## Step 1: Fetch Current Download Count 📊

Use the GitHub CLI to query the packages API and sum all version download counts:

```bash
gh api '/orgs/github/packages/container/gh-aw-mcpg/versions?per_page=100' \
  | python3 -c "
import sys, json
versions = json.load(sys.stdin)
total = sum(v.get('download_count', 0) for v in versions)
print(total)
"
```

If that command fails (e.g., permission error or package not found), also try fetching the package-level info:

```bash
gh api /orgs/github/packages/container/gh-aw-mcpg 2>&1
```

Record today's total download count as `currentCount`. Also record today's date:

```bash
date '+%Y-%m-%d'
```

## Step 2: Load Historical Data from Cache 💾

Check the cache for historical download data:

- **Cache directory**: `/tmp/gh-aw/cache-memory/ghcr-download-tracker/`
- **File**: `download-history.json`

Expected format:
```json
{
  "history": [
    {"date": "2026-01-01", "count": 1234},
    {"date": "2026-01-02", "count": 1290}
  ],
  "lastUpdated": "2026-01-02"
}
```

If the file does not exist, start with an empty `history` array.

```bash
mkdir -p /tmp/gh-aw/cache-memory/ghcr-download-tracker/
cat /tmp/gh-aw/cache-memory/ghcr-download-tracker/download-history.json 2>/dev/null || echo '{"history":[],"lastUpdated":""}'
```

## Step 3: Update History 🔄

Using Python, append today's data point to the history and keep only the last 90 days:

```bash
python3 << 'EOF'
import json, os
from datetime import datetime, timedelta

cache_dir = '/tmp/gh-aw/cache-memory/ghcr-download-tracker'
cache_file = os.path.join(cache_dir, 'download-history.json')

today = datetime.utcnow().strftime('%Y-%m-%d')
current_count = <REPLACE_WITH_currentCount>

# Load existing history
try:
    with open(cache_file) as f:
        data = json.load(f)
except (FileNotFoundError, json.JSONDecodeError):
    data = {'history': [], 'lastUpdated': ''}

history = data.get('history', [])

# Remove any existing entry for today (idempotent re-runs)
history = [e for e in history if e['date'] != today]

# Append today's entry
history.append({'date': today, 'count': current_count})

# Sort by date ascending
history.sort(key=lambda e: e['date'])

# Keep only last 90 days
cutoff = (datetime.utcnow() - timedelta(days=90)).strftime('%Y-%m-%d')
history = [e for e in history if e['date'] >= cutoff]

data = {'history': history, 'lastUpdated': today}

os.makedirs(cache_dir, exist_ok=True)
with open(cache_file, 'w') as f:
    json.dump(data, f, indent=2)

print(json.dumps(history))
EOF
```

## Step 4: Generate Chart URL 📈

Use Python to build a quickchart.io chart URL from the historical data.

Limit the chart labels to at most 30 data points — if there are more than 30 entries, sample them evenly so the chart remains readable.

```bash
python3 << 'PYEOF'
import json, urllib.parse

# Load history from cache
with open('/tmp/gh-aw/cache-memory/ghcr-download-tracker/download-history.json') as f:
    data = json.load(f)

history = data['history']

# Sample down to at most 30 points if needed
if len(history) > 30:
    step = len(history) / 30
    history = [history[int(i * step)] for i in range(30)]

labels = [e['date'] for e in history]
counts = [e['count'] for e in history]

chart_config = {
    "type": "line",
    "data": {
        "labels": labels,
        "datasets": [{
            "label": "Total Downloads",
            "data": counts,
            "borderColor": "rgb(99, 102, 241)",
            "backgroundColor": "rgba(99, 102, 241, 0.1)",
            "tension": 0.3,
            "fill": True,
            "pointRadius": 4
        }]
    },
    "options": {
        "title": {
            "display": True,
            "text": "ghcr.io/github/gh-aw-mcpg — Total Downloads Over Time"
        },
        "scales": {
            "yAxes": [{"ticks": {"beginAtZero": False}}],
            "xAxes": [{"ticks": {"maxRotation": 45, "autoSkip": True, "maxTicksLimit": 10}}]
        }
    }
}

config_str = json.dumps(chart_config, separators=(',', ':'))
encoded = urllib.parse.quote(config_str)
chart_url = f"https://quickchart.io/chart?c={encoded}&w=700&h=350&bkg=white"
print(chart_url)
PYEOF
```

Save the printed URL as `chartUrl`.

## Step 5: Compute Summary Statistics 📋

From the historical data, compute:

- **currentCount**: today's total download count
- **delta7**: growth over the last 7 days (difference between today's count and the count 7 days ago, or the earliest available)
- **delta30**: growth over the last 30 days (difference between today's count and the count 30 days ago, or the earliest available)
- **earliestDate**: the oldest date in the history array

Use Python or bash arithmetic to compute these values.

## Step 6: Build and Create the Issue 🎫

Compose the issue body following the report structure guidelines (use `###` or lower for all headers):

```markdown
### ghcr.io/github/gh-aw-mcpg — Download Statistics

![Downloads Over Time](<chartUrl>)

### Summary

| Metric | Value |
|--------|-------|
| **Current Total Downloads** | <currentCount> |
| **7-Day Growth** | +<delta7> downloads |
| **30-Day Growth** | +<delta30> downloads |
| **Tracking Since** | <earliestDate> |

### Daily Download History

<details>
<summary><b>View Full Download History</b></summary>

| Date | Total Downloads | Daily Δ |
|------|----------------|---------|
<one row per entry, most recent first, showing daily change>

</details>

**References:** Workflow run [§${{ github.run_id }}](${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }})
```

Then use the `create-issue` safe output with:

- **Title**: `ghcr.io/github/gh-aw-mcpg — Download Graph (<today's date>)`
- **Body**: the formatted report above

The `close-older-issues: true` setting will automatically close the previous report before creating the new one, so only the latest graph issue remains open.

## Step 7: Handle Errors Gracefully ⚠️

If you cannot fetch the download count due to API errors or permission issues:

1. Log the error output you received
2. Call the `noop` safe output with a descriptive message, e.g.:
   ```
   noop: "Could not fetch download stats for ghcr.io/github/gh-aw-mcpg — API error: <error message>. No issue created."
   ```
3. Do NOT create an issue when data is unavailable

## Notes

- The quickchart.io chart URL is embedded as a Markdown image in the issue body. The image renders in the user's browser — no outbound network request is needed from the workflow itself.
- Historical data accumulates daily; entries older than 90 days are pruned automatically.
- The `close-older-issues: true` setting ensures only the most recent report issue is open at any time.
- On the first run, only a single data point will be available; the graph will show a flat line, which is expected. The trend becomes visible after a few days.