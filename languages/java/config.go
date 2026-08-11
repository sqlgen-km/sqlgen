package java

// PkgCfg is a single Java package configuration.
type PkgCfg struct {
	ModelPackage     string   `yaml:"modelPackage"`
	MapperPackage    string   `yaml:"mapperPackage"`
	Out              string   `yaml:"out"`
	Files            []string `yaml:"files"`
	EngineSubPackage bool     `yaml:"engineSubPackage"`
}
