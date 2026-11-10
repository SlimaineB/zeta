package greet

import "fmt"

func Message(name string) string {
	return fmt.Sprintf("hello from %s", name)
}
