package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)


func LoadConfig(configPath string) (*Config, error) {
	if configPath == "" {
		return nil, fmt.Errorf("config path is required")
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

// validateRoles 验证角色配置，支持角色数组
func validateRoles(roles []string) error {
	if len(roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}
	
	validRoles := map[string]bool{
		"etcd":    true,
		"master":  true,  
		"worker":  true,
		"control": true, // 兼容旧的control角色
	}
	
	// 验证每个角色
	for _, role := range roles {
		role = strings.TrimSpace(strings.ToLower(role))
		if role == "" {
			continue
		}
		if !validRoles[role] {
			return fmt.Errorf("invalid role '%s', must be one of: etcd, master, worker", role)
		}
	}
	
	return nil
}

func validateConfig(config *Config) error {
	if len(config.Hosts) == 0 {
		return fmt.Errorf("at least one host must be specified")
	}

	for i, host := range config.Hosts {
		if host.IP == "" {
			return fmt.Errorf("host[%d]: IP is required", i)
		}
		if host.User == "" {
			return fmt.Errorf("host[%d]: user is required", i)
		}
		// 验证角色数组
		if err := validateRoles(host.Role); err != nil {
			return fmt.Errorf("host[%d]: %w", i, err)
		}
		if host.Password == "" && host.SSHKey == "" {
			return fmt.Errorf("host[%d]: either password or ssh_key must be specified", i)
		}
	}

	return nil
}


func (c *Config) GetControlHosts() []Host {
	var controlHosts []Host
	for _, host := range c.Hosts {
		// 检查角色数组中是否包含control角色
		for _, role := range host.Role {
			if strings.TrimSpace(strings.ToLower(role)) == "control" {
				controlHosts = append(controlHosts, host)
				break
			}
		}
	}
	return controlHosts
}

func (c *Config) GetWorkerHosts() []Host {
	var workerHosts []Host
	for _, host := range c.Hosts {
		// 检查角色数组中是否包含worker角色
		for _, role := range host.Role {
			if strings.TrimSpace(strings.ToLower(role)) == "worker" {
				workerHosts = append(workerHosts, host)
				break
			}
		}
	}
	return workerHosts
}


