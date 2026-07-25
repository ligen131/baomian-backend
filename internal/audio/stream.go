package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

type pcm16StereoReader interface {
	Read([]byte) (int, error)
}

func streamStereoPCM16(ctx context.Context, reader pcm16StereoReader, inputSampleRate int, onFrame func([]byte) error) error {
	if inputSampleRate <= 0 {
		return fmt.Errorf("invalid input sample rate %d", inputSampleRate)
	}
	return streamPCM16(ctx, reader, inputSampleRate, 2, onFrame)
}

func streamPCM16(ctx context.Context, reader io.Reader, inputSampleRate int, channels int, onFrame func([]byte) error) error {
	if inputSampleRate <= 0 || (channels != 1 && channels != 2) {
		return fmt.Errorf("unsupported PCM format: rate=%d channels=%d", inputSampleRate, channels)
	}
	bytesPerSample := channels * 2
	readBuffer := make([]byte, 32*1024)
	pending := make([]byte, 0, len(readBuffer)+bytesPerSample)
	output := make([]byte, 0, outputFrameBytes)
	var inputIndex int64
	var outputIndex int64
	var nextOutputInputIndex int64

	emit := func(sample int16) error {
		output = binary.LittleEndian.AppendUint16(output, uint16(sample))
		if len(output) != outputFrameBytes {
			return nil
		}
		if err := onFrame(output); err != nil {
			return err
		}
		output = make([]byte, 0, outputFrameBytes)
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := reader.Read(readBuffer)
		if n > 0 {
			pending = append(pending, readBuffer[:n]...)
			completeBytes := len(pending) - len(pending)%bytesPerSample
			for offset := 0; offset < completeBytes; offset += bytesPerSample {
				if err := ctx.Err(); err != nil {
					return err
				}
				for inputIndex == nextOutputInputIndex {
					left := int32(int16(binary.LittleEndian.Uint16(pending[offset : offset+2])))
					sample := left
					if channels == 2 {
						right := int32(int16(binary.LittleEndian.Uint16(pending[offset+2 : offset+4])))
						sample = (left + right) / 2
					}
					if err := emit(int16(sample)); err != nil {
						return err
					}
					outputIndex++
					nextOutputInputIndex = outputIndex * int64(inputSampleRate) / outputSampleRate
				}
				inputIndex++
			}
			copy(pending, pending[completeBytes:])
			pending = pending[:len(pending)-completeBytes]
		}
		if readErr != nil {
			if readErr != io.EOF {
				return readErr
			}
			break
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	if len(pending) != 0 {
		return io.ErrUnexpectedEOF
	}
	if len(output) > 0 {
		output = append(output, make([]byte, outputFrameBytes-len(output))...)
		return onFrame(output)
	}
	return nil
}
