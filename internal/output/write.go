package output

import (
	"encoding/json"
	"fmt"
	"os"
)

// WriteJSONFile writes v as pretty-printed JSON to path.
func WriteJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

