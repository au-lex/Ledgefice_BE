package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"time"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
)

type ImageService struct {
	cld *cloudinary.Cloudinary
	ctx context.Context
}

func NewImageService(cld *cloudinary.Cloudinary) *ImageService {
	return &ImageService{
		cld: cld,
		ctx: context.Background(),
	}
}

// Upload uploads a multipart file into the given Cloudinary folder.
// Returns (secureURL, publicID, error).
func (s *ImageService) Upload(file multipart.File, header *multipart.FileHeader, folder string) (string, string, error) {
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return "", "", fmt.Errorf("reading file: %w", err)
	}

	ext  := filepath.Ext(header.Filename)
	name := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)

	res, err := s.cld.Upload.Upload(s.ctx, bytes.NewReader(data), uploader.UploadParams{
		Folder:       folder,
		PublicID:     name[:len(name)-len(ext)],
		ResourceType: "image",
	})
	if err != nil {
		return "", "", fmt.Errorf("cloudinary upload: %w", err)
	}

	return res.SecureURL, res.PublicID, nil
}

// Delete removes an asset from Cloudinary by its public ID.
func (s *ImageService) Delete(publicID string) error {
	_, err := s.cld.Upload.Destroy(s.ctx, uploader.DestroyParams{
		PublicID: publicID,
	})
	return err
}