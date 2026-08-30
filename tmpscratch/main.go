package main

import (
	"fmt"
	"os"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/env"
)

func main() {
	os.Unsetenv("MUNDUS_API_KEY")
	os.Setenv("ANGELA_MUNDUS_API_KEY", "sk-mundus-from-angela-prefixed-env")

	resolver := config.NewShellVariableResolver(env.New())

	resolved, err := resolver.ResolveValue("$MUNDUS_API_KEY")
	fmt.Printf("mundus-anthropic api_key template: $MUNDUS_API_KEY\n")
	fmt.Printf("resolved=%q err=%v\n", resolved, err)
	if resolved == "sk-mundus-from-angela-prefixed-env" {
		fmt.Println("PASS: ANGELA_MUNDUS_API_KEY was picked up correctly")
	} else {
		fmt.Println("FAIL: ANGELA_MUNDUS_API_KEY was NOT picked up")
	}
}
