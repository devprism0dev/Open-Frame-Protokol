package ofp

import (
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"os"
)

const (
	colorReset = "\033[0m"
	colorCyan  = "\033[36m"
)

// printImageInTerminal prints an image to the terminal using ANSI color codes
func printImageInTerminal(imgPath string, maxWidth, maxHeight int) bool {
	file, err := os.Open(imgPath)
	if err != nil {
		return false
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return false
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Resmi küçült
	scaleX := float64(maxWidth) / float64(width)
	scaleY := float64(maxHeight) / float64(height)
	scale := scaleX
	if scaleY < scaleX {
		scale = scaleY
	}

	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)

	fmt.Println()
	for y := 0; y < newHeight; y++ {
		for x := 0; x < newWidth; x++ {
			srcX := int(float64(x) / scale)
			srcY := int(float64(y) / scale)
			if srcX < width && srcY < height {
				c := img.At(srcX, srcY)
				var r, g, b uint32
				switch c := c.(type) {
				case color.RGBA:
					r, g, b = uint32(c.R), uint32(c.G), uint32(c.B)
				case color.NRGBA:
					r, g, b = uint32(c.R), uint32(c.G), uint32(c.B)
				default:
					r, g, b, _ = c.RGBA()
					r, g, b = r>>8, g>>8, b>>8
				}
				// ANSI escape kodları ile renkli pixel göster
				fmt.Printf("\033[48;2;%d;%d;%dm \033[0m", r, g, b)
			}
		}
		fmt.Println()
	}
	fmt.Println()
	return true
}

// printServerBanner prints the server startup banner
func printServerBanner(addr string, logoPath string) {
	pid := os.Getpid()
	port := getPort(addr)
	host := getHost(addr)

	// logo.png'yi göster
	if logoPath != "" {
		printImageInTerminal(logoPath, 50, 20)
	}

	fmt.Printf("%sOFP Server%s\n", colorCyan, colorReset)
	fmt.Printf("Address: %s\n", addr)
	fmt.Printf("Bound on host %s and port %s\n", host, port)
	fmt.Printf("Protocol: %sOFP/3%s\n", colorCyan, colorReset)
	fmt.Printf("PID: %s%d%s\n", colorCyan, pid, colorReset)
	fmt.Println()
}

func getHost(addr string) string {
	for i := 0; i < len(addr); i++ {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return "0.0.0.0"
}

func getPort(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i+1:]
		}
	}
	return "4433"
}
