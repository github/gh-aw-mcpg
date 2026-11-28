package difc

import "github.com/github/gh-aw-mcpg/internal/logger"

var logCapabilities = logger.ForFile()

// Capabilities represents the global set of tags available in the system
// This is used to validate and discover available DIFC tags
type Capabilities struct {
	tagSet
}

// NewCapabilities creates a new empty capabilities set
func NewCapabilities() *Capabilities {
	logCapabilities.Print("Creating new capabilities set")
	return &Capabilities{tagSet: newTagSet()}
}

// Add adds a tag to the capabilities
func (c *Capabilities) Add(tag Tag) {
	logCapabilities.Printf("Adding tag: %s", tag)
	c.add(tag)
}

// AddAll adds multiple tags to the capabilities
func (c *Capabilities) AddAll(tags []Tag) {
	logCapabilities.Printf("Adding %d tags to capabilities", len(tags))
	c.addAll(tags)
}

// Contains checks if a tag is available in the capabilities
func (c *Capabilities) Contains(tag Tag) bool {
	ok := c.contains(tag)
	logCapabilities.Printf("Contains: tag=%s, found=%v", tag, ok)
	return ok
}

// GetAll returns all available tags
func (c *Capabilities) GetAll() []Tag {
	tags := c.getAll()
	logCapabilities.Printf("GetAll: returning %d tags", len(tags))
	return tags
}

// Remove removes a tag from the capabilities
func (c *Capabilities) Remove(tag Tag) {
	logCapabilities.Printf("Removing tag: %s", tag)
	c.remove(tag)
}

// Clear removes all tags from the capabilities
// It is retained as a test hook for reset-and-reuse checks; production does not
// reset the global capabilities set.
func (c *Capabilities) Clear() {
	logCapabilities.Print("Clearing all capabilities")
	c.clear()
}

// Count returns the number of available tags
func (c *Capabilities) Count() int {
	return c.count()
}
