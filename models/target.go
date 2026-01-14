package models

type Target struct {
	Name       string
	BundleName string
	BundlePath string
	In         []string
	Out        []string
	Deps       []string
	Env        map[string]string
	Cmd        string
	Config     TargetConfig
}

type TargetConfig struct {
	WorkingDir string
	Dotenv     DotenvConfig
	Reload     bool
	Ignore     []string
}

type DotenvConfig struct {
	Enabled bool
}

func (t *Target) ID() string {
	return t.BundleName + ":" + t.Name
}

func (t *Target) HasSuffix(suffix string) bool {
	return hasSuffix(t.Name, suffix)
}
