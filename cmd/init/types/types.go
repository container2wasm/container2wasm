package init

type BootConfig struct {
	Mounts    []MountInfo   `json:"mounts"`
	CmdPreRun [][]string    `json:"cmd_pre_run,omitempty"`
	Cmd       [][]string    `json:"cmd,omitempty"`
	Debug     bool          `json:"debug,omitempty"`
	DebugInit bool          `json:"debug_init,omitempty"`
	Container ContainerInfo `json:"container"`
}

type ContainerInfo struct {
	BundlePath        string `json:"bundle_path"`
	ImageConfigPath   string `json:"image_config_path"`
	RuntimeConfigPath string `json:"runtime_config_path"`
}

type MountInfo struct {
	FSType   string     `json:"fstype,omitempty"`
	Src      string     `json:"src"`
	Dst      string     `json:"dst"`
	Flags    uintptr    `json:"flags,omitempty"`
	Data     string     `json:"data,omitempty"`
	Dir      []DirInfo  `json:"dir,omitempty"`
	File     []FileInfo `json:"file,omitempty"`
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
