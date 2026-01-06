package main

import "github.com/githubnext/gh-aw-mcpg/internal/cmd"

func main() {
	cmd.SetVersion(Version)
	cmd.Execute()
}
