package core

import (
	"image"
	_ "image/gif"  // poster formats
	_ "image/jpeg" // poster formats
	_ "image/png"  // poster formats
	"os"

	_ "golang.org/x/image/bmp"  // poster formats
	_ "golang.org/x/image/webp" // poster formats
)

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
