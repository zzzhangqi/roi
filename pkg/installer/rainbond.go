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

type RainbondInstaller struct {
	config          *config.Config
	resourceManager *resource.Manager
	logger          *logrus.Logger
}

func NewRainbondInstaller(cfg *config.Config, rm *resource.Manager) *RainbondInstaller {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	
	return &RainbondInstaller{
		config:          cfg,
		resourceManager: rm,
		logger:          logger,
	}
}

func (rb *RainbondInstaller) Install(ctx context.Context) error {
	rb.logger.Info("Installing Rainbond...")

	if err := rb.waitForKubernetes(ctx); err != nil {
		return fmt.Errorf("kubernetes is not ready: %w", err)
	}

	if err := rb.installHelm(ctx); err != nil {
		return fmt.Errorf("failed to install Helm: %w", err)
	}

	return rb.installRainbond(ctx)
}

func (rb *RainbondInstaller) waitForKubernetes(ctx context.Context) error {
	rb.logger.Info("Waiting for Kubernetes to be ready...")
	
	cmd := exec.Command("kubectl", "get", "nodes")
	cmd.Env = append(cmd.Env, "KUBECONFIG=/etc/rancher/rke2/rke2.yaml")
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubernetes cluster is not ready: %w", err)
	}

	rb.logger.Info("Kubernetes cluster is ready")
	return nil
}

func (rb *RainbondInstaller) installHelm(ctx context.Context) error {
	rb.logger.Info("Installing Helm...")
	
	// Try to use offline resources first, fallback to online installation
	helmTarPath := rb.resourceManager.GetResourcePath("helm-v3.14.0-linux-amd64.tar.gz")
	if err := rb.resourceManager.ExtractTarGz(ctx, helmTarPath, "/tmp"); err != nil {
		rb.logger.Warnf("Failed to extract Helm from offline resources, trying online installation: %v", err)
		
		installCmd := exec.CommandContext(ctx, "sh", "-c", 
			"curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash")
		if err := installCmd.Run(); err != nil {
			return fmt.Errorf("failed to install Helm: %w", err)
		}
	} else {
		cpCmd := exec.CommandContext(ctx, "cp", "/tmp/linux-amd64/helm", "/usr/local/bin/helm")
		if err := cpCmd.Run(); err != nil {
			return fmt.Errorf("failed to copy Helm binary: %w", err)
		}
	}

	chmodCmd := exec.CommandContext(ctx, "chmod", "+x", "/usr/local/bin/helm")
	if err := chmodCmd.Run(); err != nil {
		return fmt.Errorf("failed to make Helm executable: %w", err)
	}

	rb.logger.Info("Helm installed successfully")
	return nil
}

func (rb *RainbondInstaller) installRainbond(ctx context.Context) error {
	namespace := "rbd-system"
	if rb.config.Rainbond.Namespace != "" {
		namespace = rb.config.Rainbond.Namespace
	}

	// Create namespace
	createNsCmd := exec.Command("kubectl", "create", "namespace", namespace, "--dry-run=client", "-o", "yaml")
	createNsCmd.Env = append(createNsCmd.Env, "KUBECONFIG=/etc/rancher/rke2/rke2.yaml")
	
	nsYaml, err := createNsCmd.Output()
	if err != nil {
		rb.logger.Warnf("Failed to generate namespace YAML: %v", err)
	} else {
		applyCmd := exec.Command("kubectl", "apply", "-f", "-")
		applyCmd.Env = append(applyCmd.Env, "KUBECONFIG=/etc/rancher/rke2/rke2.yaml")
		applyCmd.Stdin = strings.NewReader(string(nsYaml))
		if err := applyCmd.Run(); err != nil {
			rb.logger.Warnf("Failed to create namespace: %v", err)
		}
	}

	// Try to use offline chart first, fallback to online
	chartPath := rb.resourceManager.GetResourcePath("rainbond-5.17.0.tgz")
	rainbondImagesTar := rb.resourceManager.GetResourcePath("rainbond-images.tar")

	var installCmd *exec.Cmd
	if err := rb.resourceManager.LoadImages(ctx, rainbondImagesTar); err == nil {
		// Offline installation
		rb.logger.Info("Installing Rainbond using offline resources...")
		
		valuesFile := rb.generateValuesFile()
		valuesPath := filepath.Join("/tmp", "rainbond-values.yaml")
		if err := rb.writeValuesFile(valuesPath, valuesFile); err != nil {
			return fmt.Errorf("failed to write values file: %w", err)
		}

		installCmd = exec.Command("helm", "install", "rainbond", chartPath,
			"--namespace", namespace,
			"--create-namespace",
			"-f", valuesPath,
			"--wait")
	} else {
		// Online installation
		rb.logger.Info("Installing Rainbond using online resources...")
		
		addRepoCmd := exec.Command("helm", "repo", "add", "rainbond", "https://charts.goodrain.com")
		addRepoCmd.Env = append(addRepoCmd.Env, "KUBECONFIG=/etc/rancher/rke2/rke2.yaml")
		if err := addRepoCmd.Run(); err != nil {
			return fmt.Errorf("failed to add Rainbond helm repo: %w", err)
		}

		updateRepoCmd := exec.Command("helm", "repo", "update")
		updateRepoCmd.Env = append(updateRepoCmd.Env, "KUBECONFIG=/etc/rancher/rke2/rke2.yaml")
		if err := updateRepoCmd.Run(); err != nil {
			return fmt.Errorf("failed to update helm repos: %w", err)
		}

		installCmd = exec.Command("helm", "install", "rainbond", "rainbond/rainbond",
			"--namespace", namespace,
			"--create-namespace",
			"--wait")
	}

	installCmd.Env = append(installCmd.Env, "KUBECONFIG=/etc/rancher/rke2/rke2.yaml")
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("failed to install Rainbond: %w", err)
	}

	rb.logger.Info("Rainbond installed successfully")
	return nil
}

func (rb *RainbondInstaller) generateValuesFile() string {
	values := `enableHA: true

rainbondOperator:
  image:
    repository: goodrain.me/rainbond-operator
    tag: v5.17.0-release

rbd-api:
  image:
    repository: goodrain.me/rbd-api
    tag: v5.17.0-release

rbd-chaos:
  image:
    repository: goodrain.me/rbd-chaos
    tag: v5.17.0-release

rbd-eventlog:
  image:
    repository: goodrain.me/rbd-eventlog
    tag: v5.17.0-release

rbd-gateway:
  image:
    repository: goodrain.me/rbd-gateway
    tag: v5.17.0-release

rbd-monitor:
  image:
    repository: goodrain.me/rbd-monitor
    tag: v5.17.0-release

rbd-mq:
  image:
    repository: goodrain.me/rbd-mq
    tag: v5.17.0-release

rbd-webcli:
  image:
    repository: goodrain.me/rbd-webcli
    tag: v5.17.0-release

rbd-worker:
  image:
    repository: goodrain.me/rbd-worker
    tag: v5.17.0-release
`

	return values
}

func (rb *RainbondInstaller) writeValuesFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}