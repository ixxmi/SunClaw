package ops

import (
	"context"
	"time"
)

type stepFunc func(ctx context.Context) (string, error)

func runStep(ctx context.Context, name string, fn stepFunc) StepResult {
	start := time.Now()
	out, err := fn(ctx)
	res := StepResult{Name: name, Success: err == nil, Duration: time.Since(start), Output: out}
	if err != nil {
		res.ErrMessage = err.Error()
	}
	return res
}
