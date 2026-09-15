//go:build windows && amd64

package ocr

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	syswinrt "github.com/deploymenttheory/go-bindings-win32/bindings/win32/system/winrt"
	"github.com/deploymenttheory/go-bindings-winrt/bindings/runtime/winrt"
	"github.com/deploymenttheory/go-bindings-winrt/bindings/winrt/graphics/imaging"
	winrtocr "github.com/deploymenttheory/go-bindings-winrt/bindings/winrt/media/ocr"
	"github.com/deploymenttheory/go-bindings-winrt/bindings/winrt/storage/streams"

	platform "wardogs-mortar/internal/win32"
)

type request struct {
	image  *platform.Image
	result chan response
}

type response struct {
	text string
	err  error
}

// OCRProvider keeps the capture workflow independent from the Windows OCR
// implementation so a local RapidOCR/ONNX provider can be added later.
type OCRProvider interface {
	Recognize(image *platform.Image) (string, error)
	Close()
}

// Provider wraps the Windows OCR engine on one locked OS thread. WinRT
// apartment initialization is thread-local, so keeping all OCR calls on this
// worker avoids crossing an uninitialized Go thread.
type Provider struct {
	jobs   chan request
	ready  chan error
	close  sync.Once
	closed chan struct{}
}

func NewSystemProvider() (*Provider, error) {
	provider := &Provider{
		jobs:   make(chan request),
		ready:  make(chan error, 1),
		closed: make(chan struct{}),
	}
	go provider.run()
	if err := <-provider.ready; err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *Provider) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := winrt.Initialize(); err != nil {
		p.ready <- fmt.Errorf("initialize Windows Runtime: %w", err)
		close(p.closed)
		return
	}
	statics, err := winrtocr.OcrEngineStatics()
	if err != nil {
		p.ready <- fmt.Errorf("load Windows OCR: %w", err)
		close(p.closed)
		return
	}
	defer statics.Release()

	maxDimension, err := statics.MaxImageDimension()
	if err != nil {
		p.ready <- fmt.Errorf("query Windows OCR image limit: %w", err)
		close(p.closed)
		return
	}
	engine, err := statics.TryCreateFromUserProfileLanguages()
	if err != nil {
		p.ready <- fmt.Errorf("create Windows OCR engine: %w", err)
		close(p.closed)
		return
	}
	if engine == nil {
		p.ready <- fmt.Errorf("no Windows OCR language is installed")
		close(p.closed)
		return
	}
	defer engine.Release()
	p.ready <- nil

	for job := range p.jobs {
		text, err := recognize(engine, maxDimension, job.image)
		job.result <- response{text: text, err: err}
	}
	close(p.closed)
}

func (p *Provider) Recognize(image *platform.Image) (string, error) {
	if image == nil || image.Width <= 0 || image.Height <= 0 {
		return "", fmt.Errorf("empty OCR image")
	}
	result := make(chan response, 1)
	select {
	case p.jobs <- request{image: image, result: result}:
	case <-p.closed:
		return "", fmt.Errorf("Windows OCR provider is closed")
	}
	answer := <-result
	return answer.text, answer.err
}

func (p *Provider) Close() {
	p.close.Do(func() { close(p.jobs) })
	<-p.closed
}

func recognize(engine *winrtocr.IOcrEngine, maxDimension uint32, input *platform.Image) (string, error) {
	gray, width, height := prepare(input, int(maxDimension))
	buffer, err := streams.Create(uint32(len(gray)))
	if err != nil {
		return "", fmt.Errorf("create OCR buffer: %w", err)
	}
	defer buffer.Release()
	if err := buffer.SetLength(uint32(len(gray))); err != nil {
		return "", fmt.Errorf("resize OCR buffer: %w", err)
	}

	access, err := winrt.QueryInterface[syswinrt.IBufferByteAccess](unsafe.Pointer(buffer), &syswinrt.IID_IBufferByteAccess)
	if err != nil {
		return "", fmt.Errorf("access OCR buffer: %w", err)
	}
	defer access.Release()
	var destination *byte
	if err := access.Buffer(&destination); err != nil {
		return "", fmt.Errorf("map OCR buffer: %w", err)
	}
	if destination == nil {
		return "", fmt.Errorf("Windows OCR returned an empty buffer")
	}
	copy(unsafe.Slice(destination, len(gray)), gray)

	statics, err := imaging.SoftwareBitmapStatics()
	if err != nil {
		return "", fmt.Errorf("load SoftwareBitmap: %w", err)
	}
	defer statics.Release()
	bitmap, err := statics.CreateCopyFromBuffer(&buffer.IBuffer, imaging.BitmapPixelFormatGray8, int32(width), int32(height))
	if err != nil {
		return "", fmt.Errorf("create SoftwareBitmap: %w", err)
	}
	defer bitmap.Release()

	operation, err := engine.RecognizeAsync(bitmap)
	if err != nil {
		return "", fmt.Errorf("start Windows OCR: %w", err)
	}
	defer operation.Release()
	result, err := operation.Await()
	if err != nil {
		return "", fmt.Errorf("wait for Windows OCR: %w", err)
	}
	defer result.Release()
	text, err := result.Text()
	if err != nil {
		return "", fmt.Errorf("read Windows OCR result: %w", err)
	}
	return text, nil
}

// prepare converts BGRA screen pixels to a high-contrast Gray8 bitmap. Chat
// text is small, so it is enlarged; map regions remain bounded by the OCR
// engine's maximum image dimension.
func prepare(input *platform.Image, maxDimension int) ([]byte, int, int) {
	scale := 2
	if maxDimension > 0 {
		for input.Width*scale > maxDimension || input.Height*scale > maxDimension {
			scale--
			if scale <= 1 {
				scale = 1
				break
			}
		}
	}
	width, height := input.Width*scale, input.Height*scale
	result := make([]byte, width*height)
	for y := 0; y < height; y++ {
		sourceY := y / scale
		for x := 0; x < width; x++ {
			sourceX := x / scale
			index := (sourceY*input.Width + sourceX) * 4
			b, g, r := input.Pixels[index], input.Pixels[index+1], input.Pixels[index+2]
			luma := (uint16(r)*299 + uint16(g)*587 + uint16(b)*114) / 1000
			// Bright HUD text is separated from the dark panels by this
			// threshold. Keeping a binary image also removes map texture noise.
			if luma >= 125 {
				result[y*width+x] = 255
			}
		}
	}
	return result, width, height
}
