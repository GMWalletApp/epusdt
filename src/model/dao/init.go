package dao

import "fmt"

func Init() error {
	if err := DBInit(); err != nil {
		return fmt.Errorf("initialize primary database: %w", err)
	}

	if err := RuntimeInit(); err != nil {
		return fmt.Errorf("initialize runtime database: %w", err)
	}
	return nil
}
