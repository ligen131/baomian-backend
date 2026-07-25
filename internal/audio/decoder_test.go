package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"
)

func TestStreamWAVDownmixesAndResamplesToVoiceFrames(t *testing.T) {
	var samples bytes.Buffer
	for index := 0; index < 441; index++ {
		_ = binary.Write(&samples, binary.LittleEndian, int16(index))
		_ = binary.Write(&samples, binary.LittleEndian, int16(index))
	}
	wav := makePCM16WAV(44100, 2, samples.Bytes())
	var output []byte
	if err := StreamWAV(context.Background(), bytes.NewReader(wav), func(frame []byte) error {
		if len(frame) != 960 {
			t.Fatalf("frame bytes = %d", len(frame))
		}
		output = append(output, frame...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(output) != 960 {
		t.Fatalf("output bytes = %d", len(output))
	}
	if got := int16(binary.LittleEndian.Uint16(output[2:4])); got < 1 || got > 3 {
		t.Fatalf("resampled second sample = %d", got)
	}
}

func TestStreamWAVRejectsUnsupportedFormat(t *testing.T) {
	wav := makePCM16WAV(24000, 3, make([]byte, 60))
	if err := StreamWAV(context.Background(), bytes.NewReader(wav), func([]byte) error { return nil }); err == nil {
		t.Fatal("three-channel WAV was accepted")
	}
}

func TestStreamWAVDoesNotReadWholeDataChunkBeforeFirstFrame(t *testing.T) {
	pcm := make([]byte, 24000*2*2)
	wav := makePCM16WAV(24000, 2, pcm)
	reader := &limitedReadRecorder{reader: bytes.NewReader(wav), maxRead: 4096}
	stop := errors.New("stop after first frame")
	readsAtFirstFrame := 0
	err := StreamWAV(context.Background(), reader, func(frame []byte) error {
		readsAtFirstFrame = reader.bytesRead
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("StreamWAV error = %v", err)
	}
	if readsAtFirstFrame >= len(wav) {
		t.Fatalf("read %d of %d bytes before first frame", readsAtFirstFrame, len(wav))
	}
}

func TestStreamMP3DoesNotDecodeWholeFileBeforeFirstFrame(t *testing.T) {
	fixture := testMP3Fixture(t)
	reader := &limitedReadRecorder{reader: bytes.NewReader(fixture), maxRead: 4096, totalBytes: len(fixture)}
	stop := errors.New("stop after first frame")
	readsAtFirstFrame := 0
	err := StreamMP3(context.Background(), struct{ io.Reader }{reader}, func(frame []byte) error {
		if len(frame) != outputFrameBytes {
			t.Fatalf("frame bytes = %d", len(frame))
		}
		readsAtFirstFrame = reader.bytesRead
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("StreamMP3 error = %v", err)
	}
	if readsAtFirstFrame >= reader.totalBytes {
		t.Fatalf("read %d of %d bytes before first frame", readsAtFirstFrame, reader.totalBytes)
	}
}

type limitedReadRecorder struct {
	reader     *bytes.Reader
	maxRead    int
	bytesRead  int
	totalBytes int
}

func (r *limitedReadRecorder) Read(buffer []byte) (int, error) {
	if r.totalBytes == 0 {
		r.totalBytes = r.reader.Len()
	}
	if len(buffer) > r.maxRead {
		buffer = buffer[:r.maxRead]
	}
	n, err := r.reader.Read(buffer)
	r.bytesRead += n
	return n, err
}

func (r *limitedReadRecorder) ReadAt(buffer []byte, offset int64) (int, error) {
	return r.reader.ReadAt(buffer, offset)
}

func (r *limitedReadRecorder) Seek(offset int64, whence int) (int64, error) {
	return r.reader.Seek(offset, whence)
}

func testMP3Fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/mpeg2.mp3")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func makePCM16WAV(sampleRate uint32, channels uint16, pcm []byte) []byte {
	var result bytes.Buffer
	result.WriteString("RIFF")
	_ = binary.Write(&result, binary.LittleEndian, uint32(36+len(pcm)))
	result.WriteString("WAVEfmt ")
	_ = binary.Write(&result, binary.LittleEndian, uint32(16))
	_ = binary.Write(&result, binary.LittleEndian, uint16(1))
	_ = binary.Write(&result, binary.LittleEndian, channels)
	_ = binary.Write(&result, binary.LittleEndian, sampleRate)
	_ = binary.Write(&result, binary.LittleEndian, sampleRate*uint32(channels)*2)
	_ = binary.Write(&result, binary.LittleEndian, channels*2)
	_ = binary.Write(&result, binary.LittleEndian, uint16(16))
	result.WriteString("data")
	_ = binary.Write(&result, binary.LittleEndian, uint32(len(pcm)))
	result.Write(pcm)
	return result.Bytes()
}
