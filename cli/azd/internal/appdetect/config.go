package appdetect

var defaultExcludePatterns = []string{
	"**/node_modules",
	"**/[Oo]ut",
	"**/[Dd]ist",
	"**/[Bb]in",
	"**/[oO]bj",
	"**/.?*",
}

type DetectOption interface {
	apply(detectConfig) detectConfig
}

type DetectDirectoryOption interface {
	applyType(projectTypeConfig) projectTypeConfig
}

func newConfig(options ...DetectOption) detectConfig {
	c := detectConfig{
		ExcludePatterns: defaultExcludePatterns,
	}

	for _, opt := range options {
		c = opt.apply(c)
	}

	if c.noExcludeDefaults {
		c.ExcludePatterns = c.ExcludePatterns[len(defaultExcludePatterns):]
	}

	setDetectors(&c.projectTypeConfig)
	return c
}

func newDirectoryConfig(options ...DetectDirectoryOption) projectTypeConfig {
	c := projectTypeConfig{}

	for _, opt := range options {
		c = opt.applyType(c)
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
		if types[d.Type()] {
			c.detectors = append(c.detectors, d)
		}
	}
}

// detectConfig holds configuration for detection.
type detectConfig struct {
	projectTypeConfig

	IncludePatterns []string
	ExcludePatterns []string

	// Internal usage fields
	noExcludeDefaults bool
}

// projectTypeConfig holds detection configuration for project types.
type projectTypeConfig struct {
	IncludeProjectTypes []ProjectType
	ExcludeProjectTypes []ProjectType

	// Internal usage fields
	detectors []ProjectDetector
}

type includePatternsOption struct {
	patterns []string
}

func (o includePatternsOption) apply(c detectConfig) detectConfig {
	c.IncludePatterns = o.patterns
	return c
}

// Include patterns for directories scanned. The default include pattern is '**'.
// The glob pattern syntax is documented at https://pkg.go.dev/github.com/bmatcuk/doublestar/v4#Match
func WithIncludePatterns(patterns []string) includePatternsOption {
	return includePatternsOption{patterns}
}

type excludePatternsOption struct {
	patterns         []string
	overrideDefaults bool
}

func (o excludePatternsOption) apply(c detectConfig) detectConfig {
	c.noExcludeDefaults = o.overrideDefaults
	c.ExcludePatterns = append(c.ExcludePatterns, o.patterns...)
	return c
}

// Exclude patterns for directories scanned. The default exclude patterns is documented by defaultExcludePatterns,
// which loosely excludes build (e.g., **/bin), packaging (e.g., **/node_modules), and hidden directories (**/.?*).
// The directory and its subdirectories are excluded if any of the exclude patterns match.
//
// Set noDefaults to true to not have defaultExcludePatterns appended.
// The glob pattern syntax is documented at https://pkg.go.dev/github.com/bmatcuk/doublestar/v4#Match
func WithExcludePatterns(patterns []string, noDefaults bool) excludePatternsOption {
	return excludePatternsOption{patterns, noDefaults}
}

type includeProjectTypeOption struct {
	include ProjectType
}

func (o includeProjectTypeOption) applyType(c projectTypeConfig) projectTypeConfig {
	c.IncludeProjectTypes = append(c.IncludeProjectTypes, o.include)
	return c
}

func (o includeProjectTypeOption) apply(c detectConfig) detectConfig {
	c.IncludeProjectTypes = append(c.IncludeProjectTypes, o.include)
	return c
}

// WithProjectType specifies a project type to be detected. While using WithProjectType,
// only the specified project type(s) will be detected.
func WithProjectType(include ProjectType) includeProjectTypeOption {
	return includeProjectTypeOption{include}
}

type excludeProjectTypeOption struct {
	exclude ProjectType
}

func (o excludeProjectTypeOption) applyType(c projectTypeConfig) projectTypeConfig {
	c.ExcludeProjectTypes = append(c.ExcludeProjectTypes, o.exclude)
	return c
}

func (o excludeProjectTypeOption) apply(c detectConfig) detectConfig {
	c.ExcludeProjectTypes = append(c.ExcludeProjectTypes, o.exclude)
	return c
}

// WithoutProjectType specifies a project type to be excluded from detection. This can be used to filter out
// project types from the default project types provided.
func WithoutProjectType(exclude ProjectType) excludeProjectTypeOption {
	return excludeProjectTypeOption{exclude}
}
