package install

import (
	"context"
	"fmt"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	"github.com/rainbond/rainbond-offline-installer/pkg/installer"
	"github.com/rainbond/rainbond-offline-installer/pkg/resource"
	"github.com/sirupsen/logrus"
)

type Installer struct {
	config          *config.Config
	logger          *logrus.Logger
	resourceManager *resource.Manager
}

func NewInstaller(cfg *config.Config) *Installer {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	
	return &Installer{
		config:          cfg,
		logger:          logger,
		resourceManager: resource.NewManager(cfg),
	}
}

func (i *Installer) Run() error {
	ctx := context.Background()
	i.logger.Info("Starting Rainbond installation...")

	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"Install RKE2", i.installRKE2},
		{"Install Rainbond", i.installRainbond},
	}

	for _, step := range steps {
		i.logger.Infof("Executing: %s...", step.name)
		if err := step.fn(ctx); err != nil {
			i.logger.Errorf("Step failed: %s: %v", step.name, err)
			return fmt.Errorf("installation step failed: %s: %w", step.name, err)
		}
		i.logger.Infof("✓ %s completed", step.name)
	}

	i.logger.Info("Rainbond installation completed successfully!")
	return nil
}


func (i *Installer) installRKE2(ctx context.Context) error {
	rke2Installer := installer.NewRKE2Installer(i.config, i.resourceManager)
	return rke2Installer.Install(ctx)
}

func (i *Installer) installRainbond(ctx context.Context) error {
	rainbondInstaller := installer.NewRainbondInstaller(i.config, i.resourceManager)
	return rainbondInstaller.Install(ctx)
}