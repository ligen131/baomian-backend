package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	outputSampleRate = 24000
	outputFrameBytes = 960
)

type wavFormat struct {
	audioFormat   uint16
	channels      uint16
	sampleRate    uint32
	bitsPerSample uint16
}

func StreamWAV(ctx context.Context, reader io.Reader, onFrame func([]byte) error) error {
	format, data, err := readWAV(reader)
	if err != nil {
		return err
	}
	if format.audioFormat != 1 || format.bitsPerSample != 16 || (format.channels != 1 && format.channels != 2) || format.sampleRate == 0 {
		return fmt.Errorf("unsupported WAV format: pcm=%d rate=%d bits=%d channels=%d", format.audioFormat, format.sampleRate, format.bitsPerSample, format.channels)
	}
	return streamPCM16(ctx, data, int(format.sampleRate), int(format.channels), onFrame)
}

func readWAV(reader io.Reader) (wavFormat, io.Reader, error) {
	var header [12]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return wavFormat{}, nil, err
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return wavFormat{}, nil, errors.New("invalid WAV header")
	}
	var format wavFormat
	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(reader, chunkHeader[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return wavFormat{}, nil, err
		}
		size := binary.LittleEndian.Uint32(chunkHeader[4:])
		switch string(chunkHeader[:4]) {
		case "fmt ":
			if size < 16 {
				return wavFormat{}, nil, errors.New("short WAV fmt chunk")
			}
			var data [16]byte
			if _, err := io.ReadFull(reader, data[:]); err != nil {
				return wavFormat{}, nil, err
			}
			format = wavFormat{
				audioFormat:   binary.LittleEndian.Uint16(data[0:2]),
				channels:      binary.LittleEndian.Uint16(data[2:4]),
				sampleRate:    binary.LittleEndian.Uint32(data[4:8]),
				bitsPerSample: binary.LittleEndian.Uint16(data[14:16]),
			}
			if err := discardWAVChunkRemainder(reader, size-16); err != nil {
				return wavFormat{}, nil, err
			}
		case "data":
			if format.sampleRate == 0 {
				return wavFormat{}, nil, errors.New("WAV fmt chunk must precede data chunk")
			}
			return format, io.LimitReader(reader, int64(size)), nil
		default:
			if err := discardWAVChunkRemainder(reader, size); err != nil {
				return wavFormat{}, nil, err
			}
		}
		if size%2 == 1 {
			if _, err := io.CopyN(io.Discard, reader, 1); err != nil {
				return wavFormat{}, nil, err
			}
		}
	}
	return wavFormat{}, nil, errors.New("WAV fmt or data chunk missing")
}

func discardWAVChunkRemainder(reader io.Reader, size uint32) error {
	if size == 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, reader, int64(size))
	return err
}
