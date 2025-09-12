package config

type Config struct {
	Hosts    []Host         `yaml:"hosts"`
	RKE2     RKE2Config     `yaml:"rke2,omitempty"`
	Rainbond RainbondConfig `yaml:"rainbond,omitempty"`
	MySQL    MySQLConfig    `yaml:"mysql,omitempty"`
}

type Host struct {
	IP          string     `yaml:"ip"`                     // 外网IP，用于SSH连接和节点间通信
	InternalIP  string     `yaml:"internal_ip,omitempty"`  // 内网IP（备用IP）
	NodeName    string     `yaml:"node_name,omitempty"`    // 节点名称，如果不指定则自动生成
	User        string     `yaml:"user"`
	Password    string     `yaml:"password,omitempty"`
	SSHKey      string     `yaml:"ssh_key,omitempty"`
	Role        []string   `yaml:"role"`                   // Kubernetes角色：master, etcd, worker
	RbdRole     []string   `yaml:"rbd_role,omitempty"`     // Rainbond角色：rbd-gateway, rbd-chaos
	NodeTaint   []string   `yaml:"node-taint,omitempty"`   // 节点污点：key=value:effect
	MySQLMaster bool       `yaml:"mysql_master,omitempty"` // 是否为MySQL Master节点
	MySQLSlave  bool       `yaml:"mysql_slave,omitempty"`  // 是否为MySQL Slave节点
	LVMConfig   *LVMConfig `yaml:"lvm_config,omitempty"`
}

type LVMConfig struct {
	VGName    string          `yaml:"vg_name"`
	PVDevices []string        `yaml:"pv_devices"`
	LVs       []LogicalVolume `yaml:"lvs"`
}

type LogicalVolume struct {
	LVName     string `yaml:"lv_name"`
	Size       string `yaml:"size"`
	MountPoint string `yaml:"mount_point"`
}

type RKE2Config struct {
	RegistryConfig string `yaml:"registry_config,omitempty"` // containerd镜像仓库配置
}


type RainbondConfig struct {
	Version   string                 `yaml:"version,omitempty"`
	Namespace string                 `yaml:"namespace,omitempty"`
	Values    map[string]interface{} `yaml:"values,omitempty"`
}

type MySQLConfig struct {
	Enabled      bool   `yaml:"enabled,omitempty"`       // 是否启用MySQL部署
	RootPassword string `yaml:"root_password,omitempty"` // MySQL root密码
	ReplUser     string `yaml:"repl_user,omitempty"`     // 复制用户
	ReplPassword string `yaml:"repl_password,omitempty"` // 复制密码
	StorageSize  string `yaml:"storage_size,omitempty"`  // 存储大小
	DataPath     string `yaml:"data_path,omitempty"`     // 数据存储路径
}
