package difc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logPathCollections = logger.ForFile()

func (p *PathLabeledData) resolveCollections() error {
	logPathCollections.Printf("Resolving collections: itemsPaths=%d, labeledPaths=%d", len(p.PathLabels.ItemsPaths), len(p.PathLabels.LabeledPaths))
	labels := make(map[string]*PathLabelEntry, len(p.PathLabels.LabeledPaths))
	for _, label := range p.PathLabels.LabeledPaths {
		entry := label.Labels
		labels[label.Path] = &entry
	}
	seen := make(map[string]bool)
	for _, path := range p.PathLabels.ItemsPaths {
		canonical := ""
		for _, part := range splitJSONPointer(path) {
			canonical += "/" + escapeJSONPointerToken(part)
		}
		if !strings.HasPrefix(path, "/") || canonical != path || seen[path] {
			return fmt.Errorf("invalid or duplicate collection path %q", path)
		}
		seen[path] = true
	}
	for _, path := range p.PathLabels.ItemsPaths {
		for ancestor := path; strings.LastIndex(ancestor, "/") > 0; {
			ancestor = ancestor[:strings.LastIndex(ancestor, "/")]
			if seen[ancestor] {
				return fmt.Errorf("overlapping collection paths %q and %q", ancestor, path)
			}
		}
		items, err := p.extractItemsAtPath(path)
		if err != nil {
			return err
		}
		logPathCollections.Printf("Extracted %d items at collection path %s", len(items), path)
		for index, item := range items {
			itemPath := path + "/" + strconv.Itoa(index)
			entry := labels[itemPath]
			if entry == nil {
				logPathCollections.Printf("No explicit labels for %s, using default labels", itemPath)
				entry = p.PathLabels.DefaultLabels
			}
			if entry == nil {
				return fmt.Errorf("missing labels for collection item %q", itemPath)
			}
			p.resolvedItems = append(p.resolvedItems, LabeledItem{
				Data: item, Labels: p.pathEntryToResource(entry), path: itemPath,
			})
		}
	}
	p.resolved = true
	logPathCollections.Printf("Resolved %d collection items", len(p.resolvedItems))
	return nil
}

func (p *PathLabeledData) rebuildCollections(filtered []FilteredItemDetail) (interface{}, error) {
	logPathCollections.Printf("Rebuilding collections: filtered=%d, mcpWrapped=%t", len(filtered), p.IsMCPWrapped)
	denied := make(map[string]bool, len(filtered))
	for _, detail := range filtered {
		denied[detail.Item.path] = true
	}
	result := pruneJSONPaths(p.UnwrappedData, "", denied)
	if !p.IsMCPWrapped {
		return result, nil
	}
	original := p.OriginalData.(map[string]interface{})
	envelope := make(map[string]interface{}, len(original))
	for key, value := range original {
		envelope[key] = value
	}
	if original["structuredContent"] != nil {
		envelope["structuredContent"] = result
	}
	if _, ok := original["content"]; ok {
		text, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("failed to encode filtered collections: %w", err)
		}
		// Replace every original content block so no unfiltered copy survives.
		envelope["content"] = []interface{}{map[string]interface{}{
			"type": "text", "text": string(text),
		}}
	}
	return envelope, nil
}

func pruneJSONPaths(value interface{}, path string, denied map[string]bool) interface{} {
	switch value := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(value))
		for key, child := range value {
			escaped := escapeJSONPointerToken(key)
			result[key] = pruneJSONPaths(child, path+"/"+escaped, denied)
		}
		return result
	case []interface{}:
		result := make([]interface{}, 0, len(value))
		for index, child := range value {
			childPath := path + "/" + strconv.Itoa(index)
			if !denied[childPath] {
				result = append(result, pruneJSONPaths(child, childPath, denied))
			}
		}

		return result
	default:
		return value
	}
}

func escapeJSONPointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
