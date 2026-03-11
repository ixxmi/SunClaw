package ops

import "errors"

var (
	ErrOperationInProgress = errors.New("another ops operation is in progress")
	ErrPermissionDenied    = errors.New("permission denied")
	ErrConfirmRequired     = errors.New("confirmation required")
)
