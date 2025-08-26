package resource

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	"github.com/sirupsen/logrus"
)

type Manager struct {
	config *config.Config
	logger *logrus.Logger
}

func NewManager(cfg *config.Config) *Manager {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	
	return &Manager{
		config: cfg,
		logger: logger,
	}
}


func (m *Manager) LoadImages(ctx context.Context, imageTarPath string) error {
	m.logger.Infof("Loading images from %s...", imageTarPath)
	
	cmd := exec.CommandContext(ctx, "docker", "load", "-i", imageTarPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to load images: %w", err)
	}

	m.logger.Info("Images loaded successfully")
	return nil
}


func (m *Manager) DownloadFile(ctx context.Context, url, destPath string) error {
	m.logger.Infof("Downloading %s to %s...", url, destPath)
	
	cmd := exec.CommandContext(ctx, "wget", "-O", destPath, url)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download %s: %w", url, err)
	}

	m.logger.Infof("Successfully downloaded: %s", destPath)
	return nil
}

func (m *Manager) ExtractTarGz(ctx context.Context, tarPath, destDir string) error {
	m.logger.Infof("Extracting %s to %s...", tarPath, destDir)
	
	cmd := exec.CommandContext(ctx, "tar", "-xzf", tarPath, "-C", destDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract %s: %w", tarPath, err)
	}

	m.logger.Infof("Successfully extracted: %s", tarPath)
	return nil
}

func (m *Manager) GetResourcePath(resource string) string {
	// Try offline path first, fallback to temp path
	offlinePath := filepath.Join("/opt/rainbond-installer/resources", resource)
	if _, err := os.Stat(offlinePath); err == nil {
		return offlinePath
	}
	return filepath.Join("/tmp/rainbond-installer", resource)
}