package uagen

import (
	"encoding/json"
	"os"
)

// Write encodes records as indented json to path.
func Write(path string, recs []Record) error {
	buf, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}
