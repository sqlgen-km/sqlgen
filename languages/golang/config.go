package golang

// PkgCfg is a single Go package configuration.
type PkgCfg struct {
	Out   string   `yaml:"out"`
	Tags  []string `yaml:"tags"`
	Files []string `yaml:"files"`
}
