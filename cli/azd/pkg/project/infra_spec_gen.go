// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

func tempInfra(
	ctx context.Context,
	alphaFeat *alpha.FeatureManager,
	prjConfig *ProjectConfig) (*Infra, error) {
	//isEnabled := alphaFeat.IsEnabled(alpha.FeatureId("azd-operations"))
	tmpDir, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
	}

	// t, err := scaffold.Load()
	// if err != nil {
	// 	return nil, fmt.Errorf("loading scaffold templates: %w", err)
	// }

	// files, err := scaffold.ExecInfraFs(t, *infraSpec)
	// if err != nil {
	// 	return nil, fmt.Errorf("executing scaffold templates: %w", err)
	// }

	// err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
	// 	if err != nil {
	// 		return err
	// 	}

	// 	if d.IsDir() {
	// 		return nil
	// 	}

	// 	target := filepath.Join(tmpDir, path)
	// 	if err := os.MkdirAll(filepath.Dir(target), osutil.PermissionDirectoryOwnerOnly); err != nil {
	// 		return err
	// 	}

	// 	contents, err := fs.ReadFile(files, path)
	// 	if err != nil {
	// 		return err
	// 	}

	// 	return os.WriteFile(target, contents, d.Type().Perm())
	// })
	// if err != nil {
	// 	return nil, fmt.Errorf("writing infrastructure: %w", err)
	// }

	return &Infra{
		Options: provisioning.Options{
			Provider: provisioning.Bicep,
			Path:     tmpDir,
			Module:   DefaultModule,
		},
		cleanupDir: tmpDir,
	}, nil
}

func infraSpec(projectConfig *ProjectConfig) (*scaffold.InfraSpec, error) {
	infraSpec := scaffold.InfraSpec{}
	backendMapping := map[string]string{}

	for _, res := range projectConfig.Resources {
		switch res.Type {
		case ResourceTypeDbRedis:
			//TODO: reenable this
			//infraSpec.DbRedis = &scaffold.DatabaseRedis{}
		case ResourceTypeDbMongo:
			infraSpec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
				DatabaseName: res.Name,
			}
		case ResourceTypeDbPostgres:
			infraSpec.DbPostgres = &scaffold.DatabasePostgres{
				DatabaseName: res.Name,
				DatabaseUser: "pgadmin",
			}
		case ResourceTypeHostContainerApp:
			props := res.Props.(ContainerAppProps)
			svcSpec := scaffold.ServiceSpec{
				Name: res.Name,
				Port: -1,
			}

			svcSpec.Env = make(map[string]string)
			for _, envVar := range props.Env {
				if len(envVar.Value) == 0 && len(envVar.Secret) == 0 {
					return nil, fmt.Errorf(
						"environment variable %s for host %s is invalid: both value and secret are set",
						envVar.Name,
						res.Name)
				}

				if len(envVar.Value) > 0 && len(envVar.Secret) > 0 {
					return nil, fmt.Errorf(
						"environment variable %s for host %s is invalid: both value and secret are empty",
						envVar.Name,
						res.Name)
				}

				isSecret := len(envVar.Secret) > 0
				value := envVar.Value
				if isSecret {
					value = envVar.Secret
				}

				names, locations := parseEnvSubtVariables(value)
				for i, name := range names {
					expression := value[locations[i].start : locations[i].stop+1]

					// Notice the use of isSecret below:
					// We derive the "secret-ness" from it's usage.
					// This is generally correct, except for the case where:
					// - CONNECTION_STRING: ${DB_HOST}:${DB_SECRET}
					// Here, DB_HOST is not a secret, but DB_SECRET is. And yet, DB_HOST will be marked as a secrete.
					// This is a limitation of the current implementation, but it's safer to mark both as a secret above.
					setParameter(&infraSpec, scaffold.BicepName(name), expression, isSecret)
				}

				var evaluatedValue string
				if len(names) == 0 {
					evaluatedValue = "'" + value + "'"
				} else if len(names) == 1 {
					// reference the variable that describes it
					evaluatedValue = scaffold.BicepName(names[0])
				} else {
					previous := 0
					evaluatedValue = "'"
					// replace each expression with references by variable name
					for i, loc := range locations {
						evaluatedValue += value[previous:loc.start]
						evaluatedValue += "${"
						evaluatedValue += scaffold.BicepName(names[i])
						evaluatedValue += "}"
						previous = loc.stop + 1
					}
					evaluatedValue += "'"
				}

				svcSpec.Env[envVar.Name] = evaluatedValue
			}

			port := props.Port
			if port < 1 || port > 65535 {
				return nil, fmt.Errorf("port value %d for host %s must be between 1 and 65535", port, res.Name)
			}

			svcSpec.Port = port

			for _, use := range res.Uses {
				useRes, isRes := projectConfig.Resources[use]
				if isRes {
					switch useRes.Type {
					case ResourceTypeDbMongo:
						svcSpec.DbCosmosMongo = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
					case ResourceTypeDbPostgres:
						svcSpec.DbPostgres = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
					case ResourceTypeDbRedis:
						svcSpec.DbRedis = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
					}
					continue
				}

				_, ok := projectConfig.Services[use]
				if ok {
					if svcSpec.Frontend == nil {
						svcSpec.Frontend = &scaffold.Frontend{}
					}

					svcSpec.Frontend.Backends = append(svcSpec.Frontend.Backends,
						scaffold.ServiceReference{Name: use})
					backendMapping[use] = res.Name
					continue
				}

				return nil, fmt.Errorf("resource %s uses %s, which does not exist", res.Name, use)
			}

			infraSpec.Services = append(infraSpec.Services, svcSpec)
		}
	}

	// create reverse mapping
	for _, svc := range infraSpec.Services {
		if front, ok := backendMapping[svc.Name]; ok {
			if svc.Backend == nil {
				svc.Backend = &scaffold.Backend{}
			}

			svc.Backend.Frontends = append(svc.Backend.Frontends, scaffold.ServiceReference{Name: front})
		}
	}

	slices.SortFunc(infraSpec.Services, func(a, b scaffold.ServiceSpec) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &infraSpec, nil
}

func setParameter(spec *scaffold.InfraSpec, name string, value string, isSecret bool) {
	for _, parameters := range spec.Parameters {
		if parameters.Name == name { // handle existing parameter
			if isSecret && !parameters.Secret {
				// escalate the parameter to a secret
				parameters.Secret = true
			}

			// safe-guard against multiple writes on the same parameter name
			// if you run into this error, consider using a different name
			if valStr, ok := parameters.Value.(string); ok && valStr != value {
				panic(fmt.Sprintf(
					"parameter collision: parameter %s already set to %s, cannot set to %s", name, valStr, value))
			}

			return
		}
	}

	spec.Parameters = append(spec.Parameters, scaffold.Parameter{
		Name:   name,
		Value:  value,
		Type:   "string",
		Secret: isSecret,
	})
}

type location struct {
	start int
	stop  int
}

// parseEnvSubtVariables parses the envsubt expression(s) present in a string.
// substitutions.
// It works with both:
// - ${var} and ${var:=default} syntaxes
func parseEnvSubtVariables(s string) (names []string, expressions []location) {
	i := 0
	inVar := false
	inVarName := false
	name := ""
	start := 0

	for i < len(s) {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			inVar = true
			inVarName = true
			start = i
			i += len("${")
			continue
		}

		if inVar && inVarName {
			// a variable name can contain letters, digits, and underscores, and nothing else.
			if unicode.IsLetter(rune(s[i])) || unicode.IsDigit(rune(s[i])) || s[i] == '_' {
				name += string(s[i])
			} else { // a non-matching character means we've reached the end of the name
				inVarName = false
			}
		}

		if inVar && s[i] == '}' {
			inVar = false
			names = append(names, name)
			name = ""
			expressions = append(expressions, location{start, i})
		}

		i++
	}
	return
}
