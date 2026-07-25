package audio

import (
	"context"
	"fmt"
	"io"

	"github.com/hajimehoshi/go-mp3"
)

func StreamMP3(ctx context.Context, reader io.Reader, onFrame func([]byte) error) error {
	decoder, err := mp3.NewDecoder(&contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return fmt.Errorf("decode MP3: %w", err)
	}
	sampleRate := decoder.SampleRate()
	if sampleRate <= 0 {
		return fmt.Errorf("invalid MP3 sample rate %d", sampleRate)
	}
	// go-mp3 按需输出 16-bit little-endian stereo PCM。
	return streamStereoPCM16(ctx, decoder, sampleRate, onFrame)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
