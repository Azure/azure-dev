package appdetect

func newConfig(options ...DetectOption) detectConfig {
	c := detectConfig{
		defaultExcludePatterns: []string{
			"**/node_modules",
			"**/[Oo]ut",
			"**/[Dd]ist",
			"**/[Bb]in",
			"**/[oO]bj",
			"*/.*",
		},
	}

	for _, opt := range options {
		c = opt.apply(c)
	}

	if c.defaultExcludePatterns != nil {
		c.ExcludePatterns = append(c.defaultExcludePatterns, c.ExcludePatterns...)
	}

	setDetectors(&c.projectTypeConfig)
	return c
}

func newDirectoryConfig(options ...DetectDirectoryOption) projectTypeConfig {
	c := projectTypeConfig{}

	for _, opt := range options {
		c = opt.apply(c)
	}

	setDetectors(&c)
	return c
}

func setDetectors(c *projectTypeConfig) {
	types := map[ProjectType]bool{}
	if c.IncludeProjectTypes != nil {
		for _, include := range c.IncludeProjectTypes {
			types[include] = true
		}
	} else {
		for _, d := range allDetectors {
			types[d.Type()] = true
		}
	}

	if c.ExcludeProjectTypes != nil {
		for _, exclude := range c.ExcludeProjectTypes {
			types[exclude] = false
		}
	}

	c.detectors = []ProjectDetector{}
	for _, d := range allDetectors {
		if types[d.Type()] == true {
			c.detectors = append(c.detectors, d)
		}
	}
}

type DetectOption interface {
	apply(detectConfig) detectConfig
}

type DetectDirectoryOption interface {
	apply(projectTypeConfig) projectTypeConfig
}

type detectConfig struct {
	projectTypeConfig

	// Include patterns for directories scanned. If unset, all directories are scanned by default.
	IncludePatterns []string
	// Exclude patterns for directories scanned.
	// By default, build and package cache directories like **/dist, **/bin, **/node_modules are automatically excluded.
	// Any hidden directories (directories starting with '.') are also excluded.
	// Set overrideDefaults in WithExcludePatterns(patterns, overrideDefaults) to choose whether to override defaults.
	ExcludePatterns []string

	// Internal usage fields
	defaultExcludePatterns []string
}

// Config that relates to project types
type projectTypeConfig struct {
	// Project types to be detected. If unset, all known project types are included.
	IncludeProjectTypes []ProjectType
	// Project types to be excluded from detection.
	ExcludeProjectTypes []ProjectType

	// Internal usage fields
	detectors []ProjectDetector
}

type IncludePatternsOption struct {
	patterns []string
}

func (o *IncludePatternsOption) apply(c detectConfig) detectConfig {
	c.IncludePatterns = o.patterns
	return c
}

func WithIncludePatterns(patterns []string) IncludePatternsOption {
	return IncludePatternsOption{patterns}
}

type ExcludePatternsOption struct {
	patterns         []string
	overrideDefaults bool
}

func (o *ExcludePatternsOption) apply(c detectConfig) detectConfig {
	if o.overrideDefaults {
		c.defaultExcludePatterns = nil
	}

	c.ExcludePatterns = append(c.ExcludePatterns, o.patterns...)
	return c
}

func WithExcludePatterns(patterns []string, overrideDefaults bool) ExcludePatternsOption {
	return ExcludePatternsOption{patterns, overrideDefaults}
}

type IncludePython struct {
}

func (o *IncludePython) apply(c detectConfig) detectConfig {
	c.IncludeProjectTypes = append(c.IncludeProjectTypes, Python)
	return c
}

func WithPython() IncludePython {
	return IncludePython{}
}

type ExcludePython struct {
}

func (o *ExcludePython) apply(c detectConfig) detectConfig {
	c.ExcludeProjectTypes = append(c.IncludeProjectTypes, Python)
	return c
}

func WithoutPython() ExcludePython {
	return ExcludePython{}
}

type IncludeDotNet struct {
}

func (o *IncludeDotNet) apply(c detectConfig) detectConfig {
	c.IncludeProjectTypes = append(c.IncludeProjectTypes, DotNet)
	return c
}

func WithDotNet() IncludeDotNet {
	return IncludeDotNet{}
}

type ExcludeDotNet struct {
}

func (o *ExcludeDotNet) apply(c detectConfig) detectConfig {
	c.ExcludeProjectTypes = append(c.IncludeProjectTypes, DotNet)
	return c
}

func WithoutDotNet() ExcludeDotNet {
	return ExcludeDotNet{}
}

type IncludeJava struct {
}

func (o *IncludeJava) apply(c detectConfig) detectConfig {
	c.IncludeProjectTypes = append(c.IncludeProjectTypes, Java)
	return c
}

func WithJava() IncludeJava {
	return IncludeJava{}
}

type ExcludeJava struct {
}

func (o *ExcludeJava) apply(c detectConfig) detectConfig {
	c.ExcludeProjectTypes = append(c.IncludeProjectTypes, Java)
	return c
}

func WithoutJava() ExcludeJava {
	return ExcludeJava{}
}

type IncludeJavaScript struct {
}

func (o *IncludeJavaScript) apply(c detectConfig) detectConfig {
	c.IncludeProjectTypes = append(c.IncludeProjectTypes, JavaScript)
	return c
}

func WithNodeJs() IncludeJavaScript {
	return IncludeJavaScript{}
}

type ExcludeJavaScript struct {
}

func (o *ExcludeJavaScript) apply(c detectConfig) detectConfig {
	c.ExcludeProjectTypes = append(c.IncludeProjectTypes, JavaScript)
	return c
}

func WithoutNodeJs() ExcludeJavaScript {
	return ExcludeJavaScript{}
}
