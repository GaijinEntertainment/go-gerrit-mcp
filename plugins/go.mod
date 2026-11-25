// Stops the Go tooling of the repository at this directory: ./... does not enter a
// nested module, so the Go sources that npm packages carry in node_modules are
// never built, tested, or linted.
module dev.gaijin.team/go/go-gerrit-mcp/plugins

go 1.25.0
