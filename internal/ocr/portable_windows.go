//go:build windows && amd64

package ocr

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "embed"

	platform "wardogs-mortar/internal/win32"
)

const (
	rapidOCRVersion = "v0.2.0-en"
	rapidOCRMarker  = "28C59FA836BF8300E8B5B8EBD8007F5E8289097FAC53F21C368D00B3D947FF1D"
)

//go:embed assets/rapidocr-v0.2.0-win-x64.zip
var rapidOCRArchive []byte

// portableProvider runs an embedded, offline RapidOCR helper. The helper and
// its English/number models are unpacked to the user's local cache on first
// launch, which keeps distribution to a single executable.
type portableProvider struct {
	mu      sync.Mutex
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	closed  bool
}

func NewProvider() (OCRProvider, error) {
	provider, portableErr := newPortableProvider()
	if portableErr == nil {
		return provider, nil
	}

	// Keep the system provider as a fallback for an installed MSIX build.
	system, systemErr := NewSystemProvider()
	if systemErr == nil {
		return system, nil
	}
	return nil, fmt.Errorf("portable OCR: %v; Windows OCR: %v", portableErr, systemErr)
}

func newPortableProvider() (*portableProvider, error) {
	runtimeDir, err := preparePortableRuntime()
	if err != nil {
		return nil, err
	}
	executable := filepath.Join(runtimeDir, "RapidOCR-json.exe")
	models := filepath.Join(runtimeDir, "models")
	command := exec.Command(
		executable,
		"--ensureAscii=1",
		"--models="+models,
		"--det=ch_PP-OCRv3_det_infer.onnx",
		"--cls=ch_ppocr_mobile_v2.0_cls_infer.onnx",
		"--rec=rec_en_PP-OCRv3_infer.onnx",
		"--keys=dict_en.txt",
		"--doAngle=0",
		"--mostAngle=0",
		"--numThread=2",
		"--padding=24",
		"--maxSideLen=1280",
	)
	command.Dir = runtimeDir
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open RapidOCR input: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open RapidOCR output: %w", err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start RapidOCR: %w", err)
	}

	provider := &portableProvider{
		command: command,
		stdin:   stdin,
		stdout:  bufio.NewScanner(stdout),
	}
	provider.stdout.Buffer(make([]byte, 4096), 4*1024*1024)
	ready := make(chan error, 1)
	go func() { ready <- provider.waitUntilReady() }()
	select {
	case err := <-ready:
		if err != nil {
			provider.stop()
			return nil, err
		}
	case <-time.After(20 * time.Second):
		provider.stop()
		return nil, fmt.Errorf("RapidOCR startup timed out")
	}
	return provider, nil
}

func (p *portableProvider) waitUntilReady() error {
	for line := 0; line < 12 && p.stdout.Scan(); line++ {
		if strings.Contains(strings.ToLower(p.stdout.Text()), "ocr init completed") {
			return nil
		}
	}
	if err := p.stdout.Err(); err != nil {
		return fmt.Errorf("read RapidOCR startup: %w", err)
	}
	return fmt.Errorf("RapidOCR exited before it was ready")
}

func (p *portableProvider) Recognize(input *platform.Image) (string, error) {
	if input == nil || input.Width <= 0 || input.Height <= 0 {
		return "", fmt.Errorf("empty OCR image")
	}
	encoded, err := encodeOCRImage(input)
	if err != nil {
		return "", err
	}
	request, err := json.Marshal(map[string]string{"image_base64": base64.StdEncoding.EncodeToString(encoded)})
	if err != nil {
		return "", fmt.Errorf("encode RapidOCR request: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", fmt.Errorf("RapidOCR provider is closed")
	}
	if _, err := p.stdin.Write(append(request, '\n')); err != nil {
		return "", fmt.Errorf("send image to RapidOCR: %w", err)
	}
	for attempts := 0; attempts < 8 && p.stdout.Scan(); attempts++ {
		text, parsed, err := parseRapidOCRResponse([]byte(p.stdout.Text()))
		if !parsed {
			continue
		}
		return text, err
	}
	if err := p.stdout.Err(); err != nil {
		return "", fmt.Errorf("read RapidOCR result: %w", err)
	}
	return "", fmt.Errorf("RapidOCR stopped unexpectedly")
}

func (p *portableProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	p.stop()
}

func (p *portableProvider) stop() {
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.command == nil || p.command.Process == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = p.command.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = p.command.Process.Kill()
		<-done
	}
}

func encodeOCRImage(input *platform.Image) ([]byte, error) {
	scale := 1
	// Chat text is only around 16-20 pixels high at the reference resolution.
	// Enlarging small crops substantially improves recognition while keeping
	// the image inside RapidOCR's configured 1280-pixel side limit.
	if input.Height <= 160 && input.Width*2 <= 1280 && input.Height*2 <= 1280 {
		scale = 2
	}
	width, height := input.Width*scale, input.Height*scale
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source := ((y/scale)*input.Width + x/scale) * 4
			target := (y*width + x) * 4
			value.Pix[target] = input.Pixels[source+2]
			value.Pix[target+1] = input.Pixels[source+1]
			value.Pix[target+2] = input.Pixels[source]
			value.Pix[target+3] = 255
		}
	}
	var result bytes.Buffer
	if err := png.Encode(&result, value); err != nil {
		return nil, fmt.Errorf("encode OCR image: %w", err)
	}
	return result.Bytes(), nil
}

func parseRapidOCRResponse(value []byte) (string, bool, error) {
	var response struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(value, &response); err != nil {
		return "", false, nil
	}
	if response.Code == 101 {
		return "", true, fmt.Errorf("RapidOCR did not find text")
	}
	if response.Code != 100 {
		return "", true, fmt.Errorf("RapidOCR failed with code %d", response.Code)
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(response.Data, &blocks); err != nil {
		return "", true, fmt.Errorf("decode RapidOCR result: %w", err)
	}
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			lines = append(lines, text)
		}
	}
	if len(lines) == 0 {
		return "", true, fmt.Errorf("RapidOCR returned empty text")
	}
	return strings.Join(lines, "\n"), true, nil
}

func preparePortableRuntime() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate OCR cache: %w", err)
		}
	}
	destination := filepath.Join(base, "WardogsMortar", "ocr", rapidOCRVersion)
	marker := filepath.Join(destination, ".ready")
	if value, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(value)) == rapidOCRMarker {
		return destination, nil
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", fmt.Errorf("create OCR cache: %w", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(rapidOCRArchive), int64(len(rapidOCRArchive)))
	if err != nil {
		return "", fmt.Errorf("open embedded OCR runtime: %w", err)
	}
	const archiveRoot = "RapidOCR-json_v0.2.0/"
	cleanRoot := filepath.Clean(destination) + string(os.PathSeparator)
	for _, entry := range reader.File {
		name := strings.TrimPrefix(filepath.ToSlash(entry.Name), archiveRoot)
		if name == "" {
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		cleanTarget := filepath.Clean(target)
		if cleanTarget != filepath.Clean(destination) && !strings.HasPrefix(cleanTarget, cleanRoot) {
			return "", fmt.Errorf("unsafe OCR archive path: %s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(cleanTarget, 0o700); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o700); err != nil {
			return "", err
		}
		if err := extractRuntimeFile(entry, cleanTarget); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(marker, []byte(rapidOCRMarker+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("finalise OCR cache: %w", err)
	}
	return destination, nil
}

func extractRuntimeFile(entry *zip.File, target string) error {
	input, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open embedded file %s: %w", entry.Name, err)
	}
	defer input.Close()
	mode := os.FileMode(0o600)
	if strings.HasSuffix(strings.ToLower(target), ".exe") {
		mode = 0o700
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create OCR runtime file: %w", err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("extract OCR runtime file: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close OCR runtime file: %w", closeErr)
	}
	return nil
}
