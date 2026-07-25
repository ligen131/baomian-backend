package audio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type SleepService struct {
	rainPath      string
	breathingPath string
}

func NewSleepService(rainPath, breathingPath string) *SleepService {
	return &SleepService{rainPath: rainPath, breathingPath: breathingPath}
}

func (s *SleepService) Stream(ctx context.Context, guidance string, onFrame func([]byte) error) error {
	path := ""
	switch guidance {
	case "rain":
		path = s.rainPath
	case "breathing_46":
		path = s.breathingPath
	default:
		return fmt.Errorf("unsupported guidance %q", guidance)
	}
	if path == "" {
		return fmt.Errorf("guidance %q path is not configured", guidance)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open guidance %q: %w", guidance, err)
	}
	defer file.Close()
	switch filepath.Ext(path) {
	case ".wav", ".WAV":
		return StreamWAV(ctx, file, onFrame)
	case ".mp3", ".MP3":
		return StreamMP3(ctx, file, onFrame)
	default:
		return fmt.Errorf("unsupported guidance file extension %q", filepath.Ext(path))
	}
}
