package appdetect

func newConfig(options ...DetectOption) detectConfig {
	c := detectConfig{
		defaultExcludePatterns: []string{
			"**/node_modules",
			"**/[Tt]arget",
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

	setDetectors(&c.languageConfig)
	return c
}

func newDirectoryConfig(options ...DetectDirectoryOption) languageConfig {
	c := languageConfig{}

	for _, opt := range options {
		c = opt.applyLang(c)
	}

	setDetectors(&c)
	return c
}

func setDetectors(c *languageConfig) {
	languages := map[Language]bool{}
	if c.IncludeLanguages != nil {
		for _, include := range c.IncludeLanguages {
			languages[include] = true
		}
	} else {
		for _, d := range allDetectors {
			languages[d.Language()] = true
		}
	}

	if c.ExcludeLanguages != nil {
		for _, exclude := range c.ExcludeLanguages {
			languages[exclude] = false
		}
	}

	c.detectors = []projectDetector{}
	for _, d := range allDetectors {
		if languages[d.Language()] {
			c.detectors = append(c.detectors, d)
		}
	}
}

type DetectOption interface {
	apply(detectConfig) detectConfig
}

type DetectDirectoryOption interface {
	applyLang(languageConfig) languageConfig
}

type LanguageOption interface {
	DetectOption
	DetectDirectoryOption
}

type detectConfig struct {
	languageConfig

	// Exclude patterns for directories scanned.
	// By default, build and package cache directories like **/dist, **/bin, **/node_modules are automatically excluded.
	// Any hidden directories (directories starting with '.') are also excluded.
	// Set overrideDefaults in WithExcludePatterns(patterns, overrideDefaults) to choose whether to override defaults.
	ExcludePatterns []string

	// Internal usage fields
	defaultExcludePatterns []string
}

// Config that relates to project languages
type languageConfig struct {
	// Project languages to be detected. If unset, all known project languages are included.
	IncludeLanguages []Language
	// Project languages to be excluded from detection.
	ExcludeLanguages []Language

	// Internal usage fields
	detectors []projectDetector
}

type excludePatternsOptions struct {
	patterns         []string
	overrideDefaults bool
}

func (o *excludePatternsOptions) apply(c detectConfig) detectConfig {
	if o.overrideDefaults {
		c.defaultExcludePatterns = nil
	}

	c.ExcludePatterns = append(c.ExcludePatterns, o.patterns...)
	return c
}

func WithExcludePatterns(patterns []string, overrideDefaults bool) DetectOption {
	return &excludePatternsOptions{patterns, overrideDefaults}
}

type includePython struct {
}

func (o *includePython) apply(c detectConfig) detectConfig {
	c.IncludeLanguages = append(c.IncludeLanguages, Python)
	return c
}

func (o *includePython) applyLang(c languageConfig) languageConfig {
	c.IncludeLanguages = append(c.IncludeLanguages, Python)
	return c
}

func WithPython() LanguageOption {
	return &includePython{}
}

type excludePython struct {
}

func (o *excludePython) apply(c detectConfig) detectConfig {
	c.ExcludeLanguages = append(c.ExcludeLanguages, Python)
	return c
}

func (o *excludePython) applyLang(c languageConfig) languageConfig {
	c.ExcludeLanguages = append(c.ExcludeLanguages, Python)
	return c
}

func WithoutPython() LanguageOption {
	return &excludePython{}
}

type includeDotNet struct {
}

func (o *includeDotNet) apply(c detectConfig) detectConfig {
	c.IncludeLanguages = append(c.IncludeLanguages, DotNet)
	return c
}

func (o *includeDotNet) applyLang(c languageConfig) languageConfig {
	c.IncludeLanguages = append(c.IncludeLanguages, DotNet)
	return c
}

func WithDotNet() LanguageOption {
	return &includeDotNet{}
}

type excludeDotNet struct {
}

func (o *excludeDotNet) apply(c detectConfig) detectConfig {
	c.ExcludeLanguages = append(c.ExcludeLanguages, DotNet)
	return c
}

func (o *excludeDotNet) applyLang(c languageConfig) languageConfig {
	c.ExcludeLanguages = append(c.ExcludeLanguages, DotNet)
	return c
}

func WithoutDotNet() LanguageOption {
	return &excludeDotNet{}
}

type includeJava struct {
}

func (o *includeJava) apply(c detectConfig) detectConfig {
	c.IncludeLanguages = append(c.IncludeLanguages, Java)
	return c
}

func (o *includeJava) applyLang(c languageConfig) languageConfig {
	c.IncludeLanguages = append(c.IncludeLanguages, Java)
	return c
}

func WithJava() LanguageOption {
	return &includeJava{}
}

type excludeJava struct {
}

func (o *excludeJava) apply(c detectConfig) detectConfig {
	c.ExcludeLanguages = append(c.ExcludeLanguages, Java)
	return c
}

func (o *excludeJava) applyLang(c languageConfig) languageConfig {
	c.ExcludeLanguages = append(c.ExcludeLanguages, Java)
	return c
}

func WithoutJava() LanguageOption {
	return &excludeJava{}
}

type includeJavaScript struct {
}

func (o *includeJavaScript) apply(c detectConfig) detectConfig {
	c.IncludeLanguages = append(c.IncludeLanguages, JavaScript)
	return c
}

func (o *includeJavaScript) applyLang(c languageConfig) languageConfig {
	c.IncludeLanguages = append(c.IncludeLanguages, JavaScript)
	return c
}

func WithJavaScript() LanguageOption {
	return &includeJavaScript{}
}

type excludeJavaScript struct {
}

func (o *excludeJavaScript) apply(c detectConfig) detectConfig {
	c.ExcludeLanguages = append(c.ExcludeLanguages, JavaScript)
	return c
}

func (o *excludeJavaScript) applyLang(c languageConfig) languageConfig {
	c.ExcludeLanguages = append(c.ExcludeLanguages, JavaScript)
	return c
}

func WithoutJavaScript() LanguageOption {
	return &excludeJavaScript{}
}
