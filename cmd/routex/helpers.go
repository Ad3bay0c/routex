package main

import "encoding/json"

// marshalJSON wraps json.MarshalIndent for consistent pretty-printed
// output across all commands that use --json output mode.
func marshalJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
