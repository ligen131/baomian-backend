package service

import "context"

type SleepAudio interface {
	Stream(ctx context.Context, guidance string, onFrame func([]byte) error) error
}
