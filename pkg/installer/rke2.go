package installer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	"github.com/rainbond/rainbond-offline-installer/pkg/resource"
	"github.com/sirupsen/logrus"
)

type RKE2Installer struct {
	config          *config.Config
	resourceManager *resource.Manager
	logger          *logrus.Logger
}

func NewRKE2Installer(cfg *config.Config, rm *resource.Manager) *RKE2Installer {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	
	return &RKE2Installer{
		config:          cfg,
		resourceManager: rm,
		logger:          logger,
	}
}

func (r *RKE2Installer) Install(ctx context.Context) error {
	r.logger.Info("Installing RKE2...")
	return r.installRKE2(ctx)
}

func (r *RKE2Installer) installRKE2(ctx context.Context) error {
	version := "v1.28.8+rke2r1"

	// Try to use offline resources first, fallback to download
	rke2TarPath := r.resourceManager.GetResourcePath("rke2.linux-amd64.tar.gz")
	if err := r.resourceManager.ExtractTarGz(ctx, rke2TarPath, "/usr/local"); err != nil {
		r.logger.Warnf("Failed to extract RKE2 from offline resources, trying to download: %v", err)
		
		rke2URL := fmt.Sprintf("https://github.com/rancher/rke2/releases/download/%s/rke2.linux-amd64.tar.gz", version)
		downloadPath := filepath.Join("/tmp", "rke2.linux-amd64.tar.gz")

		if err := r.resourceManager.DownloadFile(ctx, rke2URL, downloadPath); err != nil {
			return fmt.Errorf("failed to download RKE2: %w", err)
		}

		if err := r.resourceManager.ExtractTarGz(ctx, downloadPath, "/usr/local"); err != nil {
			return fmt.Errorf("failed to extract RKE2: %w", err)
		}
	}

	// Try to load images from offline resources
	imagesTarPath := r.resourceManager.GetResourcePath("rke2-images.linux-amd64.tar.zst")
	if err := r.loadRKE2Images(ctx, imagesTarPath); err != nil {
		r.logger.Warnf("Failed to load RKE2 images from offline resources: %v", err)
	}

	return r.configureAndStartRKE2(ctx)
}

func (r *RKE2Installer) loadRKE2Images(ctx context.Context, imagesTarPath string) error {
	r.logger.Info("Loading RKE2 images...")
	
	cmd := exec.CommandContext(ctx, "zstd", "-d", imagesTarPath, "-o", "/tmp/rke2-images.tar")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to decompress RKE2 images: %w", err)
	}

	if err := r.resourceManager.LoadImages(ctx, "/tmp/rke2-images.tar"); err != nil {
		return fmt.Errorf("failed to load RKE2 images: %w", err)
	}


	return nil
}

func (r *RKE2Installer) configureAndStartRKE2(ctx context.Context) error {
	r.logger.Info("Configuring RKE2...")

	if err := os.MkdirAll("/etc/rancher/rke2", 0755); err != nil {
		return fmt.Errorf("failed to create RKE2 config directory: %w", err)
	}

	configContent := r.generateRKE2Config()
	if err := os.WriteFile("/etc/rancher/rke2/config.yaml", []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write RKE2 config: %w", err)
	}

	if err := r.createSystemdService(); err != nil {
		return fmt.Errorf("failed to create RKE2 systemd service: %w", err)
	}

	startCmd := exec.CommandContext(ctx, "systemctl", "enable", "rke2-server")
	if err := startCmd.Run(); err != nil {
		return fmt.Errorf("failed to enable RKE2 service: %w", err)
	}

	startCmd = exec.CommandContext(ctx, "systemctl", "start", "rke2-server")
	if err := startCmd.Run(); err != nil {
		return fmt.Errorf("failed to start RKE2 service: %w", err)
	}

	r.logger.Info("RKE2 installed and started successfully")
	return nil
}

func (r *RKE2Installer) generateRKE2Config() string {
	var config strings.Builder
	
	config.WriteString("write-kubeconfig-mode: \"0644\"\n")
	config.WriteString("tls-san:\n")
	
	for _, host := range r.config.GetControlHosts() {
		config.WriteString(fmt.Sprintf("  - %s\n", host.IP))
	}



	return config.String()
}

func (r *RKE2Installer) createSystemdService() error {
	serviceContent := `[Unit]
Description=Rancher Kubernetes Engine v2 (server)
Documentation=https://rancher.com/docs/rke2
Wants=network-online.target
After=network-online.target
Conflicts=rke2-agent.service

[Install]
WantedBy=multi-user.target

[Service]
Type=notify
EnvironmentFile=-/etc/default/%N
EnvironmentFile=-/etc/sysconfig/%N
EnvironmentFile=-/usr/local/lib/systemd/system/rke2-server.env
KillMode=process
Delegate=yes
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0
Restart=always
RestartSec=5s
ExecStartPre=/bin/sh -xc '! /usr/bin/systemctl is-enabled --quiet nm-cloud-setup.service'
ExecStartPre=-/sbin/modprobe br_netfilter
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/rke2 server
`

	return os.WriteFile("/etc/systemd/system/rke2-server.service", []byte(serviceContent), 0644)
}