// Package all imports every built-in Routex tool sub-package, triggering
// their init() functions so all 11 tools are registered automatically.
//
// Import this package when you want all built-in tools available without
// listing them individually:
//
//	import _ "github.com/Ad3bay0c/routex/tools/all"
//
// This is the convenience import — it pulls in every tool's dependencies
// (HTTP clients, API SDKs, etc.) whether you use them or not.
//
// For leaner binaries, import only the sub-packages you need:
//
//	import (
//	    _ "github.com/Ad3bay0c/routex/tools/file"    // read_file, write_file
//	    _ "github.com/Ad3bay0c/routex/tools/search"  // web_search, brave_search, wikipedia
//	    _ "github.com/Ad3bay0c/routex/tools/web"     // http_request, read_url, scrape
//	    // omit tools/ai and tools/comms to avoid those dependencies
//	)
package all

import (
	// Each blank import triggers that sub-package's init() functions,
	// registering its tools in the global built-in registry.
	_ "github.com/Ad3bay0c/routex/tools/ai"
	_ "github.com/Ad3bay0c/routex/tools/comms"
	_ "github.com/Ad3bay0c/routex/tools/file"
	_ "github.com/Ad3bay0c/routex/tools/search"
	_ "github.com/Ad3bay0c/routex/tools/web"
)
