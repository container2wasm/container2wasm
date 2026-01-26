package init

type BootConfig struct {
	Mounts     []MountInfo   `json:"mounts"`
	CmdPreRun  [][]string    `json:"cmd_pre_run,omitempty"`
	Cmd        [][]string    `json:"cmd,omitempty"`
	Debug      bool          `json:"debug,omitempty"`
	DebugInit  bool          `json:"debug_init,omitempty"`
	Container  ContainerInfo `json:"container"`
	PostMounts []MountInfo   `json:"post_mounts"`
	WasiP2Mode bool          `json:"wasi_p2_mode,omitempty"`
}

type ContainerInfo struct {
	BundlePath        string `json:"bundle_path"`
	ImageConfigPath   string `json:"image_config_path"`
	ImageRootfsPath   string `json:"image_rootfs_path"`
	RuntimeConfigPath string `json:"runtime_config_path"`
	ExternalBundle    bool   `json:"external_bundle"`
}

type MountInfo struct {
	FSType   string     `json:"fstype,omitempty"`
	Src      string     `json:"src"`
	Dst      string     `json:"dst"`
	Flags    uintptr    `json:"flags,omitempty"`
	Data     string     `json:"data,omitempty"`
	Dir      []DirInfo  `json:"dir,omitempty"`
	File     []FileInfo `json:"file,omitempty"`
	PostDir  []DirInfo  `json:"post_dir,omitempty"`
	PostFile []FileInfo `json:"post_file,omitempty"`
	Cmd      []string   `json:"cmd,omitempty"`
	Async    bool       `json:"async,omitempty"`
	Optional bool       `json:"optional,omitempty"`
}

type DirInfo struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
}

type FileInfo struct {
	Path     string `json:"path"`
	Mode     uint32 `json:"mode"`
	Contents string `json:"contents,omitempty"`
}
