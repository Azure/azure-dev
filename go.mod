module github.com/azure/azure-dev

go 1.18

require (
	github.com/AlecAivazis/survey/v2 v2.3.5
	github.com/blang/semver/v4 v4.0.0
	github.com/drone/envsubst v1.0.3
	github.com/fatih/color v1.13.0
	github.com/joho/godotenv v1.4.0
	github.com/magefile/mage v1.12.1
	github.com/mattn/go-isatty v0.0.14
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d
	github.com/otiai10/copy v1.7.0
	github.com/pbnj/go-open v0.1.1
	github.com/sethvargo/go-retry v0.2.3
	github.com/spf13/cobra v1.3.0
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.8.0
	github.com/theckman/yacspin v0.13.12
	go.uber.org/multierr v1.8.0
	golang.org/x/exp v0.0.0-20220428152302-39d4317da171
	golang.org/x/sys v0.0.0-20220422013727-9388b58f7150
	gopkg.in/yaml.v3 v3.0.1
)

// Using temporal survey fork that supports features like
// - hint in page footer
// - customizible help tittle
replace github.com/AlecAivazis/survey/v2 => github.com/vhvb1989/survey/v2 v2.5.0

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-runewidth v0.0.13 // indirect
	github.com/pkg/errors v0.8.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	go.uber.org/atomic v1.9.0 // indirect
	golang.org/x/term v0.0.0-20220526004731-065cf7ba2467 // indirect
	golang.org/x/text v0.3.7 // indirect
)
