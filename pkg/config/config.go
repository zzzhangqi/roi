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

	// 后处理配置：自动从hosts中设置gateway和chaos节点
	config.PostProcessConfig()
	
	// 设置默认的Component配置
	config.SetDefaultComponentConfig()
	
	// 设置默认的Rainbond配置
	config.SetDefaultRainbondConfig()
	
	// 设置默认的MySQL配置
	config.SetDefaultMySQLConfig()

	return &config, nil
}

// SaveConfig 保存配置到文件
func SaveConfig(config *Config, configPath string) error {
	if configPath == "" {
		return fmt.Errorf("config path is required")
	}

	// 获取绝对路径
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// 序列化配置为YAML
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(absPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
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

// validateRbdRoles 验证Rainbond角色配置
func validateRbdRoles(rbdRoles []string) error {
	if len(rbdRoles) == 0 {
		return nil // rbd_role是可选的
	}
	
	validRbdRoles := map[string]bool{
		"rbd-gateway": true,
		"rbd-chaos":   true,
	}
	
	// 验证每个Rainbond角色
	for _, role := range rbdRoles {
		role = strings.TrimSpace(strings.ToLower(role))
		if role == "" {
			continue
		}
		if !validRbdRoles[role] {
			return fmt.Errorf("invalid rbd_role '%s', must be one of: rbd-gateway, rbd-chaos", role)
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
		// 验证Rainbond角色数组
		if err := validateRbdRoles(host.RbdRole); err != nil {
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

func (c *Config) GetRbdGatewayHosts() []Host {
	var gatewayHosts []Host
	for _, host := range c.Hosts {
		// 检查rbd_role数组中是否包含rbd-gateway角色
		for _, role := range host.RbdRole {
			if strings.TrimSpace(strings.ToLower(role)) == "rbd-gateway" {
				gatewayHosts = append(gatewayHosts, host)
				break
			}
		}
	}
	return gatewayHosts
}

func (c *Config) GetRbdChaosHosts() []Host {
	var chaosHosts []Host
	for _, host := range c.Hosts {
		// 检查rbd_role数组中是否包含rbd-chaos角色
		for _, role := range host.RbdRole {
			if strings.TrimSpace(strings.ToLower(role)) == "rbd-chaos" {
				chaosHosts = append(chaosHosts, host)
				break
			}
		}
	}
	return chaosHosts
}

// PostProcessConfig 后处理配置，自动从hosts中设置nodesForGateway和nodesForChaos，并设置默认Cluster配置
func (c *Config) PostProcessConfig() {
	// 确保rainbond.values存在
	if c.Rainbond.Values == nil {
		c.Rainbond.Values = make(map[string]interface{})
	}
	
	// 确保Cluster配置存在
	cluster, exists := c.Rainbond.Values["Cluster"]
	if !exists {
		cluster = make(map[string]interface{})
		c.Rainbond.Values["Cluster"] = cluster
	}
	
	clusterMap := cluster.(map[string]interface{})
	
	// 设置默认的containerdRuntimePath（如果不存在）
	if _, exists := clusterMap["containerdRuntimePath"]; !exists {
		clusterMap["containerdRuntimePath"] = "/var/run/k3s/containerd"
	}
	
	// 获取带有rbd-gateway和rbd-chaos角色的节点
	gatewayHosts := c.GetRbdGatewayHosts()
	chaosHosts := c.GetRbdChaosHosts()
	
	// 构建nodesForGateway（如果有gateway节点）
	if len(gatewayHosts) > 0 {
		var nodesForGateway []map[string]interface{}
		
		for _, host := range gatewayHosts {
			// 使用InternalIP作为主要IP，如果没有则使用IP
			internalIP := host.InternalIP
			if internalIP == "" {
				internalIP = host.IP
			}
			
			nodeInfo := map[string]interface{}{
				"name":       host.IP, // 使用外网IP作为节点名称
				"externalIP": host.IP,
				"internalIP": internalIP,
			}
			
			nodesForGateway = append(nodesForGateway, nodeInfo)
		}
		
		// 设置nodesForGateway
		clusterMap["nodesForGateway"] = nodesForGateway
	}
	
	// 构建nodesForChaos（如果有chaos节点）
	if len(chaosHosts) > 0 {
		var nodesForChaos []map[string]interface{}
		for _, host := range chaosHosts {
			chaosNode := map[string]interface{}{
				"name": host.IP, // 使用外网IP作为节点名称
			}
			nodesForChaos = append(nodesForChaos, chaosNode)
		}
		
		// 设置nodesForChaos
		clusterMap["nodesForChaos"] = nodesForChaos
	}
	
	// 设置gatewayIngressIPs（自动从第一个rbd-gateway节点获取）
	if len(gatewayHosts) > 0 {
		// 使用第一个gateway节点的IP作为gatewayIngressIPs
		clusterMap["gatewayIngressIPs"] = gatewayHosts[0].IP
	}
}

// SetDefaultComponentConfig 设置默认的组件配置
func (c *Config) SetDefaultComponentConfig() {
	// 确保rainbond.values存在
	if c.Rainbond.Values == nil {
		c.Rainbond.Values = make(map[string]interface{})
	}
	
	// 确保Component配置存在
	component, exists := c.Rainbond.Values["Component"]
	if !exists {
		component = make(map[string]interface{})
		c.Rainbond.Values["Component"] = component
	}
	
	componentMap := component.(map[string]interface{})
	
	// 确保rbd_app_ui配置存在
	rbdAppUI, exists := componentMap["rbd_app_ui"]
	if !exists {
		rbdAppUI = make(map[string]interface{})
		componentMap["rbd_app_ui"] = rbdAppUI
	}
	
	rbdAppUIMap := rbdAppUI.(map[string]interface{})
	
	// 确保env配置存在
	env, exists := rbdAppUIMap["env"]
	if !exists {
		env = []interface{}{}
		rbdAppUIMap["env"] = env
	}
	
	envList := env.([]interface{})
	
	// 检查是否已经存在DISABLE_DEFAULT_APP_MARKET配置
	hasDisableDefaultAppMarket := false
	for _, envVar := range envList {
		if envMap, ok := envVar.(map[string]interface{}); ok {
			if name, exists := envMap["name"]; exists && name == "DISABLE_DEFAULT_APP_MARKET" {
				hasDisableDefaultAppMarket = true
				break
			}
		}
	}
	
	// 如果不存在，添加默认的DISABLE_DEFAULT_APP_MARKET配置
	if !hasDisableDefaultAppMarket {
		defaultEnv := map[string]interface{}{
			"name":  "DISABLE_DEFAULT_APP_MARKET",
			"value": "true",
		}
		envList = append(envList, defaultEnv)
		rbdAppUIMap["env"] = envList
	}
}

// SetDefaultRainbondConfig 设置默认的Rainbond配置
func (c *Config) SetDefaultRainbondConfig() {
	// 设置默认的namespace（如果不存在）
	if c.Rainbond.Namespace == "" {
		c.Rainbond.Namespace = "rbd-system"
	}
}

// SetDefaultMySQLConfig 设置默认的MySQL配置，根据主机配置自动判断是否启用MySQL
func (c *Config) SetDefaultMySQLConfig() {
	// 检查是否有节点配置了MySQL主从角色
	hasMySQLNodes := false
	for _, host := range c.Hosts {
		if host.MySQLMaster || host.MySQLSlave {
			hasMySQLNodes = true
			break
		}
	}
	
	// 如果有MySQL节点，自动启用MySQL（用户可以显式设置enabled: false来覆盖）
	if hasMySQLNodes {
		c.MySQL.Enabled = true
	}
	
	// 注意：这个逻辑意味着如果用户在配置文件中写了 enabled: false，
	// 这里会被覆盖为true。如果需要支持用户禁用，需要更复杂的逻辑。
}

// IsMySQLEnabled 检查MySQL是否应该启用（基于节点配置）
func (c *Config) IsMySQLEnabled() bool {
	for _, host := range c.Hosts {
		if host.MySQLMaster || host.MySQLSlave {
			return true
		}
	}
	return false
}

// GetMySQLMasterHosts 获取MySQL主节点
func (c *Config) GetMySQLMasterHosts() []Host {
	var masters []Host
	for _, host := range c.Hosts {
		if host.MySQLMaster {
			masters = append(masters, host)
		}
	}
	return masters
}

// GetMySQLSlaveHosts 获取MySQL从节点
func (c *Config) GetMySQLSlaveHosts() []Host {
	var slaves []Host
	for _, host := range c.Hosts {
		if host.MySQLSlave {
			slaves = append(slaves, host)
		}
	}
	return slaves
}


