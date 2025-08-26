package lvm

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	"github.com/sirupsen/logrus"
)

type LVM struct {
	config *config.Config
	logger *logrus.Logger
}

type LVMStatus struct {
	IP         string
	Role       []string
	VGName     string
	PVDevices  []string
	LVs        []string
	Status     string
	DeviceInfo string
	// 新增字段
	VGSize    string      // 卷组总大小
	VGUsed    string      // 卷组已用空间
	LVDetails []LVInfo    // 逻辑卷详细信息
	MountInfo []MountInfo // 挂载信息
}

type LVInfo struct {
	Name       string
	Size       string
	Used       string
	MountPoint string
	Status     string
}

type MountInfo struct {
	Device     string
	MountPoint string
	Size       string
	Used       string
	Available  string
	Usage      string
}

func NewLVM(cfg *config.Config) *LVM {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)

	return &LVM{
		config: cfg,
		logger: logger,
	}
}

// Show 显示 LVM 状态
func (l *LVM) Show() error {
	l.logger.Info("Showing LVM status...")

	hasLVMConfig := false
	for _, host := range l.config.Hosts {
		if host.LVMConfig != nil && len(host.LVMConfig.PVDevices) > 0 {
			hasLVMConfig = true
			break
		}
	}

	if !hasLVMConfig {
		l.logger.Info("No LVM configuration found on any host.")
		return nil
	}

	results := make(map[string]*LVMStatus)
	for _, host := range l.config.Hosts {
		results[host.IP] = &LVMStatus{
			IP:     host.IP,
			Role:   host.Role,
			Status: "Unknown",
		}
	}

	// 检查 LVM 工具
	for i, host := range l.config.Hosts {
		if host.LVMConfig == nil {
			continue
		}

		l.logger.Infof("Checking LVM tools for host %s...", host.IP)

		sshCmd := l.buildSSHCommand(host, "which lvm")
		if err := sshCmd.Run(); err != nil {
			results[host.IP].Status = "Failed"
			return fmt.Errorf("host[%d] %s: LVM tools not found, please install lvm2 package", i, host.IP)
		}
	}

	// 检查 PV 设备
	for i, host := range l.config.Hosts {
		if host.LVMConfig == nil {
			results[host.IP].DeviceInfo = "No LVM Config"
			continue
		}

		l.logger.Infof("Checking LVM devices for host %s...", host.IP)

		vgName := host.LVMConfig.VGName
		if vgName == "" {
			vgName = "vg_rainbond"
		}

		results[host.IP].VGName = vgName
		results[host.IP].PVDevices = host.LVMConfig.PVDevices

		var lvNames []string
		for _, lv := range host.LVMConfig.LVs {
			lvNames = append(lvNames, lv.LVName)
		}
		results[host.IP].LVs = lvNames

		deviceList := []string{}
		for _, device := range host.LVMConfig.PVDevices {
			// 通过 SSH 检查远程设备
			sshCmd := l.buildSSHCommand(host, fmt.Sprintf("test -e %s", device))
			if err := sshCmd.Run(); err != nil {
				results[host.IP].Status = "Failed"
				return fmt.Errorf("host[%d] %s: LVM device %s not found", i, host.IP, device)
			}

			// 检查设备大小
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lsblk -b -d -n -o SIZE %s 2>/dev/null | head -1", device))
			_, err := sshCmd.Output()
			if err == nil {
				deviceList = append(deviceList, fmt.Sprintf("%s", device))
			} else {
				deviceList = append(deviceList, device)
			}
		}

		results[host.IP].DeviceInfo = fmt.Sprintf("%d devices found", len(deviceList))
	}

	// 检查 LVM 状态
	for _, host := range l.config.Hosts {
		if host.LVMConfig == nil {
			continue
		}

		l.logger.Infof("Checking LVM status for host %s...", host.IP)

		vgName := host.LVMConfig.VGName
		if vgName == "" {
			vgName = "vg_rainbond"
		}

		// 检查卷组是否存在
		sshCmd := l.buildSSHCommand(host, fmt.Sprintf("vgs %s --noheadings --nosuffix --units g", vgName))
		_, err := sshCmd.Output()
		if err != nil {
			l.logger.Warnf("Host %s: Volume group %s not found or not accessible", host.IP, vgName)
			l.logger.Infof("Host %s: You may need to create the volume group first", host.IP)
			results[host.IP].Status = "Not Created"
			continue
		}

		// 检查逻辑卷是否存在
		allLVsExist := true
		for _, lv := range host.LVMConfig.LVs {
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lvs %s/%s --noheadings --nosuffix --units g", vgName, lv.LVName))
			_, err := sshCmd.Output()
			if err != nil {
				l.logger.Warnf("Host %s: Logical volume %s/%s not found", host.IP, vgName, lv.LVName)
				l.logger.Infof("Host %s: You may need to create the logical volume %s", host.IP, lv.LVName)
				allLVsExist = false
			} else {
				l.logger.Infof("Host %s: Logical volume %s/%s exists", host.IP, vgName, lv.LVName)
			}
		}

		if allLVsExist {
			results[host.IP].Status = "Ready"
		} else {
			results[host.IP].Status = "Partial"
		}

		// 收集卷组详细信息
		sshCmd = l.buildSSHCommand(host, fmt.Sprintf("vgs %s --noheadings --units g --nosuffix", vgName))
		vgsOutput, _ := sshCmd.Output()
		if len(vgsOutput) > 0 {
			fields := strings.Fields(strings.TrimSpace(string(vgsOutput)))
			if len(fields) >= 6 {
				results[host.IP].VGSize = fields[5] + "G"
				results[host.IP].VGUsed = fields[4] + "G"
			}
			l.logger.Infof("Host %s: Current volume groups:\n%s", host.IP, string(vgsOutput))
		}

		// 收集逻辑卷详细信息
		var lvDetails []LVInfo
		for _, lv := range host.LVMConfig.LVs {
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lvs %s/%s --noheadings --units g --nosuffix", vgName, lv.LVName))
			lvOutput, _ := sshCmd.Output()
			if len(lvOutput) > 0 {
				fields := strings.Fields(strings.TrimSpace(string(lvOutput)))
				if len(fields) >= 3 {
					mountPoint := l.getMountPoint(lv.LVName, &lv)
					lvInfo := LVInfo{
						Name:       lv.LVName,
						Size:       fields[3] + "G",
						Used:       fields[3] + "G", // LVM 逻辑卷通常全部分配
						MountPoint: mountPoint,
						Status:     "Active",
					}
					lvDetails = append(lvDetails, lvInfo)
				}
			}
		}
		results[host.IP].LVDetails = lvDetails

		// 收集挂载详细信息
		var mountInfos []MountInfo
		for _, lv := range host.LVMConfig.LVs {
			mountPoint := l.getMountPoint(lv.LVName, &lv)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("df -h %s", mountPoint))
			dfOutput, _ := sshCmd.Output()
			if len(dfOutput) > 0 {
				lines := strings.Split(strings.TrimSpace(string(dfOutput)), "\n")
				if len(lines) > 1 {
					fields := strings.Fields(lines[1])
					if len(fields) >= 5 {
						mountInfo := MountInfo{
							Device:     fmt.Sprintf("/dev/%s/%s", vgName, lv.LVName),
							MountPoint: mountPoint,
							Size:       fields[1],
							Used:       fields[2],
							Available:  fields[3],
							Usage:      fields[4],
						}
						mountInfos = append(mountInfos, mountInfo)
					}
				}
			}
		}
		results[host.IP].MountInfo = mountInfos

		// 显示挂载信息
		sshCmd = l.buildSSHCommand(host, "df -h | grep -E '(docker|containerd)'")
		mountOutput, _ := sshCmd.Output()
		if len(mountOutput) > 0 {
			l.logger.Infof("Host %s: Mounted volumes:\n%s", host.IP, string(mountOutput))
		}
	}

	l.printResultsTable(results)
	return nil
}

// Create 创建 LVM 配置
func (l *LVM) Create() error {
	l.logger.Info("Creating LVM configuration...")

	hasLVMConfig := false
	for _, host := range l.config.Hosts {
		if host.LVMConfig != nil && len(host.LVMConfig.PVDevices) > 0 {
			hasLVMConfig = true
			break
		}
	}

	if !hasLVMConfig {
		return fmt.Errorf("no LVM configuration found in any host")
	}

	for i, host := range l.config.Hosts {
		if host.LVMConfig == nil {
			continue
		}

		l.logger.Infof("Creating LVM configuration for host %s...", host.IP)

		// 检查 LVM 工具
		sshCmd := l.buildSSHCommand(host, "which lvm")
		if err := sshCmd.Run(); err != nil {
			return fmt.Errorf("host[%d] %s: LVM tools not found, please install lvm2 package", i, host.IP)
		}

		vgName := host.LVMConfig.VGName
		if vgName == "" {
			vgName = "vg_rainbond"
		}

		// 检查设备是否存在
		for _, device := range host.LVMConfig.PVDevices {
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("test -e %s", device))
			if err := sshCmd.Run(); err != nil {
				return fmt.Errorf("host[%d] %s: LVM device %s not found", i, host.IP, device)
			}
		}

		// 创建物理卷
		for _, device := range host.LVMConfig.PVDevices {
			l.logger.Infof("Host %s: Creating physical volume on %s", host.IP, device)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("pvcreate %s", device))
			if err := sshCmd.Run(); err != nil {
				l.logger.Warnf("Host %s: Physical volume %s may already exist", host.IP, device)
			}
		}

		// 创建卷组
		l.logger.Infof("Host %s: Creating volume group %s", host.IP, vgName)
		deviceList := strings.Join(host.LVMConfig.PVDevices, " ")
		sshCmd = l.buildSSHCommand(host, fmt.Sprintf("vgcreate %s %s", vgName, deviceList))
		if err := sshCmd.Run(); err != nil {
			l.logger.Warnf("Host %s: Volume group %s may already exist", host.IP, vgName)
		}

		// 创建逻辑卷
		for _, lv := range host.LVMConfig.LVs {
			l.logger.Infof("Host %s: Creating logical volume %s with size %s", host.IP, lv.LVName, lv.Size)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lvcreate -n %s -L %s %s", lv.LVName, lv.Size, vgName))
			if err := sshCmd.Run(); err != nil {
				l.logger.Warnf("Host %s: Logical volume %s may already exist", host.IP, lv.LVName)
			}
		}

		// 格式化并挂载逻辑卷
		for _, lv := range host.LVMConfig.LVs {
			l.logger.Infof("Host %s: Formatting logical volume %s", host.IP, lv.LVName)

			// 格式化文件系统 (使用 XFS)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("mkfs.xfs /dev/%s/%s", vgName, lv.LVName))
			if err := sshCmd.Run(); err != nil {
				l.logger.Warnf("Host %s: Logical volume %s may already be formatted", host.IP, lv.LVName)
			}

			// 创建挂载点
			mountPoint := l.getMountPoint(lv.LVName, &lv)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("mkdir -p %s", mountPoint))
			sshCmd.Run() // 忽略错误，目录可能已存在

			// 挂载逻辑卷
			l.logger.Infof("Host %s: Mounting %s to %s", host.IP, lv.LVName, mountPoint)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("mount /dev/%s/%s %s", vgName, lv.LVName, mountPoint))
			if err := sshCmd.Run(); err != nil {
				l.logger.Warnf("Host %s: Logical volume %s may already be mounted", host.IP, lv.LVName)
			}

			// 添加到 /etc/fstab（避免重复添加）
			l.logger.Infof("Host %s: Adding %s to /etc/fstab", host.IP, lv.LVName)
			fstabEntry := fmt.Sprintf("/dev/%s/%s %s xfs defaults 0 0", vgName, lv.LVName, mountPoint)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("grep -q '%s' /etc/fstab || echo '%s' >> /etc/fstab", fstabEntry, fstabEntry))
			if err := sshCmd.Run(); err != nil {
				l.logger.Warnf("Host %s: Failed to add %s to /etc/fstab", host.IP, lv.LVName)
			}
		}

		l.logger.Infof("Host %s: LVM configuration completed", host.IP)
	}

	l.logger.Info("LVM configuration creation completed!")
	return nil
}

// ShowAndCreate 合并显示状态和创建配置功能
func (l *LVM) ShowAndCreate() error {
	l.logger.Info("显示LVM状态并创建配置...")

	hasLVMConfig := false
	for _, host := range l.config.Hosts {
		if host.LVMConfig != nil && len(host.LVMConfig.PVDevices) > 0 {
			hasLVMConfig = true
			break
		}
	}

	if !hasLVMConfig {
		l.logger.Info("未在任何主机上找到LVM配置")
		return nil
	}

	results := make(map[string]*LVMStatus)
	for _, host := range l.config.Hosts {
		results[host.IP] = &LVMStatus{
			IP:     host.IP,
			Role:   host.Role,
			Status: "Unknown",
		}
	}

	// 先显示当前状态
	l.logger.Info("=== 当前LVM状态 ===")
	l.checkCurrentStatus(results)

	// 然后创建配置
	l.logger.Info("=== 创建LVM配置 ===")
	if err := l.createLVMConfiguration(); err != nil {
		return err
	}

	// 最后显示创建后的状态
	l.logger.Info("=== 最终LVM状态 ===")
	l.checkCurrentStatus(results)

	l.printVerticalResultsTable(results)
	return nil
}

// checkCurrentStatus 检查当前 LVM 状态
func (l *LVM) checkCurrentStatus(results map[string]*LVMStatus) error {
	// 检查 LVM 工具
	for i, host := range l.config.Hosts {
		if host.LVMConfig == nil {
			continue
		}

		l.logger.Infof("Checking LVM tools for host %s...", host.IP)

		sshCmd := l.buildSSHCommand(host, "which lvm")
		if err := sshCmd.Run(); err != nil {
			results[host.IP].Status = "Failed"
			return fmt.Errorf("host[%d] %s: LVM tools not found, please install lvm2 package", i, host.IP)
		}
	}

	// 检查 PV 设备
	for i, host := range l.config.Hosts {
		if host.LVMConfig == nil {
			results[host.IP].DeviceInfo = "No LVM Config"
			continue
		}

		l.logger.Infof("Checking LVM devices for host %s...", host.IP)

		vgName := host.LVMConfig.VGName
		if vgName == "" {
			vgName = "vg_rainbond"
		}

		results[host.IP].VGName = vgName
		results[host.IP].PVDevices = host.LVMConfig.PVDevices

		var lvNames []string
		for _, lv := range host.LVMConfig.LVs {
			lvNames = append(lvNames, lv.LVName)
		}
		results[host.IP].LVs = lvNames

		deviceList := []string{}
		for _, device := range host.LVMConfig.PVDevices {
			// 通过 SSH 检查远程设备
			sshCmd := l.buildSSHCommand(host, fmt.Sprintf("test -e %s", device))
			if err := sshCmd.Run(); err != nil {
				results[host.IP].Status = "Failed"
				return fmt.Errorf("host[%d] %s: LVM device %s not found", i, host.IP, device)
			}

			// 检查设备大小
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lsblk -b -d -n -o SIZE %s 2>/dev/null | head -1", device))
			_, err := sshCmd.Output()
			if err == nil {
				deviceList = append(deviceList, fmt.Sprintf("%s", device))
			} else {
				deviceList = append(deviceList, device)
			}
		}

		results[host.IP].DeviceInfo = fmt.Sprintf("%d devices found", len(deviceList))
	}

	// 检查 LVM 状态
	for _, host := range l.config.Hosts {
		if host.LVMConfig == nil {
			continue
		}

		l.logger.Infof("Checking LVM status for host %s...", host.IP)

		vgName := host.LVMConfig.VGName
		if vgName == "" {
			vgName = "vg_rainbond"
		}

		// 检查卷组是否存在
		sshCmd := l.buildSSHCommand(host, fmt.Sprintf("vgs %s --noheadings --nosuffix --units g", vgName))
		_, err := sshCmd.Output()
		if err != nil {
			l.logger.Warnf("Host %s: Volume group %s not found or not accessible", host.IP, vgName)
			results[host.IP].Status = "Not Created"
			continue
		}

		// 检查逻辑卷是否存在
		allLVsExist := true
		for _, lv := range host.LVMConfig.LVs {
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lvs %s/%s --noheadings --nosuffix --units g", vgName, lv.LVName))
			_, err := sshCmd.Output()
			if err != nil {
				l.logger.Warnf("Host %s: Logical volume %s/%s not found", host.IP, vgName, lv.LVName)
				allLVsExist = false
			} else {
				l.logger.Infof("Host %s: Logical volume %s/%s exists", host.IP, vgName, lv.LVName)
			}
		}

		if allLVsExist {
			results[host.IP].Status = "Ready"
		} else {
			results[host.IP].Status = "Partial"
		}

		// 收集卷组详细信息
		sshCmd = l.buildSSHCommand(host, fmt.Sprintf("vgs %s --noheadings --units g --nosuffix", vgName))
		vgsOutput, _ := sshCmd.Output()
		if len(vgsOutput) > 0 {
			fields := strings.Fields(strings.TrimSpace(string(vgsOutput)))
			// vgs输出格式: VG #PV #LV #SN Attr VSize VFree
			if len(fields) >= 7 {
				vgSize := fields[5]     // VSize (总大小)
				vgFree := fields[6]     // VFree (可用空间)
				results[host.IP].VGSize = vgSize + "G"
				
				// 计算已用空间 = 总大小 - 可用空间
				if vgSizeFloat, err := strconv.ParseFloat(vgSize, 64); err == nil {
					if vgFreeFloat, err := strconv.ParseFloat(vgFree, 64); err == nil {
						vgUsed := vgSizeFloat - vgFreeFloat
						results[host.IP].VGUsed = fmt.Sprintf("%.2fG", vgUsed)
					} else {
						results[host.IP].VGUsed = "未知"
					}
				} else {
					results[host.IP].VGUsed = "未知"
				}
			}
		}

		// 收集逻辑卷详细信息
		var lvDetails []LVInfo
		for _, lv := range host.LVMConfig.LVs {
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lvs %s/%s --noheadings --units g --nosuffix", vgName, lv.LVName))
			lvOutput, _ := sshCmd.Output()
			if len(lvOutput) > 0 {
				fields := strings.Fields(strings.TrimSpace(string(lvOutput)))
				if len(fields) >= 3 {
					mountPoint := l.getMountPoint(lv.LVName, &lv)
					lvInfo := LVInfo{
						Name:       lv.LVName,
						Size:       fields[3] + "G",
						Used:       fields[3] + "G", // LVM 逻辑卷通常全部分配
						MountPoint: mountPoint,
						Status:     "Active",
					}
					lvDetails = append(lvDetails, lvInfo)
				}
			}
		}
		results[host.IP].LVDetails = lvDetails

		// 收集挂载详细信息
		var mountInfos []MountInfo
		for _, lv := range host.LVMConfig.LVs {
			mountPoint := l.getMountPoint(lv.LVName, &lv)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("df -h %s 2>/dev/null", mountPoint))
			dfOutput, _ := sshCmd.Output()
			if len(dfOutput) > 0 {
				lines := strings.Split(strings.TrimSpace(string(dfOutput)), "\n")
				if len(lines) > 1 {
					fields := strings.Fields(lines[1])
					if len(fields) >= 5 {
						mountInfo := MountInfo{
							Device:     fmt.Sprintf("/dev/%s/%s", vgName, lv.LVName),
							MountPoint: mountPoint,
							Size:       fields[1],
							Used:       fields[2],
							Available:  fields[3],
							Usage:      fields[4],
						}
						mountInfos = append(mountInfos, mountInfo)
					}
				}
			}
		}
		results[host.IP].MountInfo = mountInfos
	}

	return nil
}

// createLVMConfiguration 创建 LVM 配置
func (l *LVM) createLVMConfiguration() error {
	for i, host := range l.config.Hosts {
		if host.LVMConfig == nil {
			continue
		}

		l.logger.Infof("主机 %s: 开始创建LVM配置...", host.IP)

		// 检查 LVM 工具
		sshCmd := l.buildSSHCommand(host, "which lvm")
		if err := sshCmd.Run(); err != nil {
			return fmt.Errorf("主机[%d] %s: 未找到LVM工具，请安装lvm2软件包", i, host.IP)
		}

		vgName := host.LVMConfig.VGName
		if vgName == "" {
			vgName = "vg_rainbond"
		}

		// 检查设备是否存在
		for _, device := range host.LVMConfig.PVDevices {
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("test -e %s", device))
			if err := sshCmd.Run(); err != nil {
				return fmt.Errorf("主机[%d] %s: LVM设备 %s 不存在", i, host.IP, device)
			}
		}

		// 创建物理卷
		for _, device := range host.LVMConfig.PVDevices {
			// 先检查物理卷是否已存在
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("pvs %s --noheadings 2>/dev/null", device))
			if err := sshCmd.Run(); err == nil {
				l.logger.Infof("主机 %s: 物理卷 %s 已存在，跳过创建", host.IP, device)
				continue
			}

			l.logger.Infof("主机 %s: 创建物理卷 %s", host.IP, device)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("pvcreate %s", device))
			output, err := sshCmd.CombinedOutput()
			if err != nil {
				if strings.Contains(string(output), "already a physical volume") {
					l.logger.Infof("主机 %s: 物理卷 %s 已存在", host.IP, device)
				} else {
					return fmt.Errorf("主机[%d] %s: 创建物理卷 %s 失败: %v - %s", 
						i, host.IP, device, err, strings.TrimSpace(string(output)))
				}
			} else {
				l.logger.Infof("主机 %s: 成功创建物理卷 %s", host.IP, device)
			}
		}

		// 创建卷组
		// 先检查卷组是否已存在
		sshCmd = l.buildSSHCommand(host, fmt.Sprintf("vgs %s --noheadings 2>/dev/null", vgName))
		if err := sshCmd.Run(); err == nil {
			l.logger.Infof("主机 %s: 卷组 %s 已存在，跳过创建", host.IP, vgName)
		} else {
			l.logger.Infof("主机 %s: 创建卷组 %s", host.IP, vgName)
			deviceList := strings.Join(host.LVMConfig.PVDevices, " ")
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("vgcreate %s %s", vgName, deviceList))
			output, err := sshCmd.CombinedOutput()
			if err != nil {
				if strings.Contains(string(output), "already exists") {
					l.logger.Infof("主机 %s: 卷组 %s 已存在", host.IP, vgName)
				} else {
					return fmt.Errorf("主机[%d] %s: 创建卷组 %s 失败: %v - %s", 
						i, host.IP, vgName, err, strings.TrimSpace(string(output)))
				}
			} else {
				l.logger.Infof("主机 %s: 成功创建卷组 %s", host.IP, vgName)
			}
		}

		// 创建逻辑卷
		for _, lv := range host.LVMConfig.LVs {
			// 先检查逻辑卷是否已存在
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lvs %s/%s --noheadings 2>/dev/null", vgName, lv.LVName))
			if err := sshCmd.Run(); err == nil {
				l.logger.Infof("主机 %s: 逻辑卷 %s 已存在，跳过创建", host.IP, lv.LVName)
				continue
			}

			l.logger.Infof("主机 %s: 创建逻辑卷 %s，大小 %s", host.IP, lv.LVName, lv.Size)
			
			// 获取可用空间信息
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("vgs %s --noheadings --units g --nosuffix -o vg_free", vgName))
			freeOutput, err := sshCmd.Output()
			if err != nil {
				return fmt.Errorf("主机[%d] %s: 无法获取卷组 %s 的可用空间信息: %v", i, host.IP, vgName, err)
			}
			
			freeSpaceStr := strings.TrimSpace(string(freeOutput))
			l.logger.Infof("主机 %s: 卷组 %s 可用空间: %s GB", host.IP, vgName, freeSpaceStr)
			
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("lvcreate -n %s -L %s %s", lv.LVName, lv.Size, vgName))
			output, err := sshCmd.CombinedOutput()
			if err != nil {
				if strings.Contains(string(output), "not enough free space") || 
				   strings.Contains(string(output), "insufficient free space") {
					return fmt.Errorf("主机[%d] %s: 创建逻辑卷 %s 失败 - 空间不足。请求大小: %s，可用空间: %s GB。请调整配置文件中的逻辑卷大小", 
						i, host.IP, lv.LVName, lv.Size, freeSpaceStr)
				} else if strings.Contains(string(output), "already exists") {
					l.logger.Infof("主机 %s: 逻辑卷 %s 已存在", host.IP, lv.LVName)
				} else {
					return fmt.Errorf("主机[%d] %s: 创建逻辑卷 %s 失败: %v - %s", 
						i, host.IP, lv.LVName, err, strings.TrimSpace(string(output)))
				}
			} else {
				l.logger.Infof("主机 %s: 成功创建逻辑卷 %s", host.IP, lv.LVName)
			}
		}

		// 格式化并挂载逻辑卷
		for _, lv := range host.LVMConfig.LVs {
			devicePath := fmt.Sprintf("/dev/%s/%s", vgName, lv.LVName)
			mountPoint := l.getMountPoint(lv.LVName, &lv)

			// 检查逻辑卷是否存在
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("test -e %s", devicePath))
			if err := sshCmd.Run(); err != nil {
				l.logger.Warnf("主机 %s: 逻辑卷设备 %s 不存在，跳过格式化和挂载", host.IP, devicePath)
				continue
			}

			// 检查文件系统是否已存在
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("blkid %s", devicePath))
			output, err := sshCmd.Output()
			if err != nil || !strings.Contains(string(output), "xfs") {
				l.logger.Infof("主机 %s: 格式化逻辑卷 %s 为XFS文件系统", host.IP, lv.LVName)
				sshCmd = l.buildSSHCommand(host, fmt.Sprintf("mkfs.xfs -f %s", devicePath))
				output, err := sshCmd.CombinedOutput()
				if err != nil {
					l.logger.Warnf("主机 %s: 格式化逻辑卷 %s 失败: %v - %s", 
						host.IP, lv.LVName, err, strings.TrimSpace(string(output)))
				} else {
					l.logger.Infof("主机 %s: 成功格式化逻辑卷 %s", host.IP, lv.LVName)
				}
			} else {
				l.logger.Infof("主机 %s: 逻辑卷 %s 已格式化为XFS，跳过格式化", host.IP, lv.LVName)
			}

			// 创建挂载点
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("mkdir -p %s", mountPoint))
			sshCmd.Run() // 忽略错误，目录可能已存在

			// 检查是否已挂载
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("mountpoint -q %s", mountPoint))
			if err := sshCmd.Run(); err == nil {
				l.logger.Infof("主机 %s: 挂载点 %s 已被挂载，跳过挂载", host.IP, mountPoint)
			} else {
				// 挂载逻辑卷
				l.logger.Infof("主机 %s: 挂载逻辑卷 %s 到 %s", host.IP, lv.LVName, mountPoint)
				sshCmd = l.buildSSHCommand(host, fmt.Sprintf("mount %s %s", devicePath, mountPoint))
				output, err := sshCmd.CombinedOutput()
				if err != nil {
					l.logger.Warnf("主机 %s: 挂载逻辑卷 %s 失败: %v - %s", 
						host.IP, lv.LVName, err, strings.TrimSpace(string(output)))
				} else {
					l.logger.Infof("主机 %s: 成功挂载逻辑卷 %s", host.IP, lv.LVName)
				}
			}

			// 添加到 /etc/fstab（避免重复添加）
			fstabEntry := fmt.Sprintf("%s %s xfs defaults 0 0", devicePath, mountPoint)
			sshCmd = l.buildSSHCommand(host, fmt.Sprintf("grep -q '%s' /etc/fstab", fstabEntry))
			if err := sshCmd.Run(); err != nil {
				l.logger.Infof("主机 %s: 添加 %s 到 /etc/fstab", host.IP, lv.LVName)
				sshCmd = l.buildSSHCommand(host, fmt.Sprintf("echo '%s' >> /etc/fstab", fstabEntry))
				if err := sshCmd.Run(); err != nil {
					l.logger.Warnf("主机 %s: 添加 %s 到 /etc/fstab 失败", host.IP, lv.LVName)
				} else {
					l.logger.Infof("主机 %s: 成功添加 %s 到 /etc/fstab", host.IP, lv.LVName)
				}
			} else {
				l.logger.Infof("主机 %s: %s 已存在于 /etc/fstab 中", host.IP, lv.LVName)
			}
		}

		l.logger.Infof("主机 %s: LVM配置完成", host.IP)
	}

	return nil
}

// printVerticalResultsTable 打印纵向 LVM 状态表格
func (l *LVM) printVerticalResultsTable(results map[string]*LVMStatus) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("                        LVM 配置结果")
	fmt.Println(strings.Repeat("=", 80))

	// 统计信息
	ready := 0
	failed := 0
	partial := 0
	noConfig := 0

	// 为每个主机打印一个纵向的信息块
	for i, host := range l.config.Hosts {
		if i > 0 {
			fmt.Println() // 主机间空行
		}
		
		result := results[host.IP]

		// 格式化显示设备列表
		pvDevices := "无"
		if len(result.PVDevices) > 0 {
			pvDevices = strings.Join(result.PVDevices, ", ")
		}

		// 格式化显示 LV 列表
		lvNames := "无"
		if len(result.LVs) > 0 {
			lvNames = strings.Join(result.LVs, ", ")
		}

		// 状态显示
		statusStr := result.Status
		statusIcon := "✓"
		switch result.Status {
		case "Failed":
			statusIcon = "✗"
			failed++
		case "Ready":
			statusIcon = "✓"
			ready++
		case "Partial":
			statusIcon = "⚠"
			partial++
		case "Not Created":
			statusIcon = "○"
			partial++
		default:
			statusIcon = "−"
			noConfig++
		}

		// 打印主机信息块
		fmt.Printf("┌─ 主机 #%d %s %s\n", i+1, statusIcon, statusStr)
		fmt.Printf("│  IP地址        : %s\n", result.IP)
		fmt.Printf("│  角色          : %s\n", strings.Join(result.Role, ","))
		fmt.Printf("│  卷组名称      : %s\n", result.VGName)
		fmt.Printf("│  物理卷设备    : %s\n", pvDevices)
		fmt.Printf("│  逻辑卷名称    : %s\n", lvNames)
		fmt.Printf("│  设备信息      : %s\n", result.DeviceInfo)
		if result.VGSize != "" {
			fmt.Printf("│  卷组大小      : %s\n", result.VGSize)
			fmt.Printf("│  已用空间      : %s\n", result.VGUsed)
		}

		// 显示逻辑卷详细信息
		if len(result.LVDetails) > 0 {
			fmt.Printf("│  逻辑卷详情    :\n")
			for _, lv := range result.LVDetails {
				fmt.Printf("│    - %s: %s -> %s (%s)\n", lv.Name, lv.Size, lv.MountPoint, lv.Status)
			}
		}

		// 显示挂载详细信息
		if len(result.MountInfo) > 0 {
			fmt.Printf("│  挂载信息      :\n")
			for _, mount := range result.MountInfo {
				fmt.Printf("│    - %s: %s/%s 可用:%s 使用率:%s\n", 
					mount.MountPoint, mount.Used, mount.Size, mount.Available, mount.Usage)
			}
		}

		fmt.Printf("└" + strings.Repeat("─", 50))
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("配置总结: %d 个主机就绪, %d 个主机部分完成, %d 个主机失败, %d 个主机无配置\n", 
		ready, partial, failed, noConfig)
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println()
}

// buildSSHCommand 构建 SSH 命令
func (l *LVM) buildSSHCommand(host config.Host, command string) *exec.Cmd {
	var sshCmd *exec.Cmd

	if host.Password != "" {
		// 检查 sshpass 是否可用
		if _, err := exec.LookPath("sshpass"); err != nil {
			l.logger.Warnf("sshpass not found. Please install sshpass or use SSH key authentication for host %s", host.IP)
			sshCmd = exec.Command("ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "BatchMode=no",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		} else {
			sshCmd = exec.Command("sshpass", "-p", host.Password, "ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		}
	} else if host.SSHKey != "" {
		sshCmd = exec.Command("ssh",
			"-i", host.SSHKey,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s", host.User, host.IP),
			command)
	} else {
		sshCmd = exec.Command("ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s", host.User, host.IP),
			command)
	}

	return sshCmd
}

// getMountPoint 根据逻辑卷名称获取挂载点
func (l *LVM) getMountPoint(lvName string, configLV *config.LogicalVolume) string {
	// 如果配置中指定了挂载点，使用配置的
	if configLV != nil && configLV.MountPoint != "" {
		return configLV.MountPoint
	}

	// 否则使用默认挂载点
	switch lvName {
	case "lv_docker":
		return "/var/lib/docker"
	case "lv_containerd":
		return "/var/lib/containerd"
	default:
		return fmt.Sprintf("/mnt/%s", lvName)
	}
}

// printResultsTable 打印 LVM 状态表格
func (l *LVM) printResultsTable(results map[string]*LVMStatus) {
	fmt.Println("\n" + strings.Repeat("=", 120))
	fmt.Println("                               LVM STATUS")
	fmt.Println(strings.Repeat("=", 120))

	// 表头
	fmt.Printf("%-15s %-8s %-15s %-30s %-25s %-15s\n",
		"IP", "Role", "VG Name", "PV Devices", "LV Names", "Device Info")
	fmt.Println(strings.Repeat("-", 120))

	// 数据行
	for _, host := range l.config.Hosts {
		result := results[host.IP]

		// 格式化显示设备列表
		pvDevices := "None"
		if len(result.PVDevices) > 0 {
			pvDevices = strings.Join(result.PVDevices, ", ")
			if len(pvDevices) > 28 {
				pvDevices = pvDevices[:25] + "..."
			}
		}

		// 格式化显示 LV 列表
		lvNames := "None"
		if len(result.LVs) > 0 {
			lvNames = strings.Join(result.LVs, ", ")
			if len(lvNames) > 23 {
				lvNames = lvNames[:20] + "..."
			}
		}

		fmt.Printf("%-15s %-8s %-15s %-30s %-25s %-15s\n",
			result.IP,
			strings.Join(result.Role, ","),
			result.VGName,
			pvDevices,
			lvNames,
			result.DeviceInfo)
	}

	fmt.Println(strings.Repeat("=", 120))

	// 详细信息表格
	fmt.Println("\n" + strings.Repeat("=", 120))
	fmt.Println("                               DETAILED LVM INFORMATION")
	fmt.Println(strings.Repeat("=", 120))

	for _, host := range l.config.Hosts {
		result := results[host.IP]
		if result.VGName == "" {
			continue
		}

		fmt.Printf("\nHost: %s (%s)\n", result.IP, strings.Join(result.Role, ","))
		fmt.Printf("Volume Group: %s (Size: %s, Used: %s)\n", result.VGName, result.VGSize, result.VGUsed)
		fmt.Println(strings.Repeat("-", 80))

		// 逻辑卷详细信息
		if len(result.LVDetails) > 0 {
			fmt.Printf("%-20s %-10s %-15s %-25s %-10s\n",
				"Logical Volume", "Size", "Mount Point", "Device", "Status")
			fmt.Println(strings.Repeat("-", 80))
			for _, lv := range result.LVDetails {
				fmt.Printf("%-20s %-10s %-15s %-25s %-10s\n",
					lv.Name, lv.Size, lv.MountPoint, fmt.Sprintf("/dev/%s/%s", result.VGName, lv.Name), lv.Status)
			}
		}

		// 挂载详细信息
		if len(result.MountInfo) > 0 {
			fmt.Printf("\n%-30s %-20s %-10s %-10s %-10s %-10s\n",
				"Device", "Mount Point", "Size", "Used", "Available", "Usage")
			fmt.Println(strings.Repeat("-", 90))
			for _, mount := range result.MountInfo {
				fmt.Printf("%-30s %-20s %-10s %-10s %-10s %-10s\n",
					mount.Device, mount.MountPoint, mount.Size, mount.Used, mount.Available, mount.Usage)
			}
		}
	}

	fmt.Println(strings.Repeat("=", 120))

	// 统计信息
	ready := 0
	failed := 0
	partial := 0
	noConfig := 0
	for _, result := range results {
		switch result.Status {
		case "Ready":
			ready++
		case "Failed":
			failed++
		case "Partial":
			partial++
		default:
			noConfig++
		}
	}

	fmt.Printf("Summary: %d ready, %d partial, %d failed, %d no config\n", ready, partial, failed, noConfig)
	fmt.Println()
}
