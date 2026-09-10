package fx

import (
	"context"
	"fmt"
	"reflect"
)

// Tool is a process owned by Server. Start must return once the tool is ready,
// Done reports an unexpected stop, and Shutdown must honor ctx.
type Tool interface {
	Name() string
	Start(ctx context.Context) error
	Done() <-chan error
	Shutdown(ctx context.Context) error
}

func validateTools(tools []Tool) error {
	names := make(map[string]struct{}, len(tools))
	for i, tool := range tools {
		if isNilTool(tool) {
			return fmt.Errorf("fiber: tool %d is nil", i)
		}

		name := tool.Name()
		if name == "" {
			return fmt.Errorf("fiber: tool %d has no name", i)
		}

		if _, exists := names[name]; exists {
			return fmt.Errorf("fiber: duplicate tool %q", name)
		}

		names[name] = struct{}{}
	}

	return nil
}

func isNilTool(tool Tool) bool {
	if tool == nil {
		return true
	}

	value := reflect.ValueOf(tool)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
