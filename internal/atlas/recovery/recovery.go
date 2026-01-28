package recovery

import (
	"bufio"
	"fmt"
	"os"
)

// Replay replays commands from an AOF file
// apply is called for each command line
func Replay(path string, apply func(string) error) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No AOF file yet, that's OK
		}
		return fmt.Errorf("failed to open AOF: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if len(line) == 0 {
			continue // skip empty lines
		}
		if err := apply(line); err != nil {
			return fmt.Errorf("failed to apply command at line %d: %w", lineNum, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading AOF: %w", err)
	}

	return nil
}

// Validate checks if an AOF file is valid
func Validate(path string, apply func(string) error) error {
	return Replay(path, apply)
}
