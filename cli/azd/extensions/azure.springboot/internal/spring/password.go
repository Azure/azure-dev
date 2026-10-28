// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"unicode"
)

const generatedPasswordLength = 32

func validatePostgreSQLPassword(password string) error {
	if len(password) < 8 || len(password) > 128 {
		return validationError(
			"invalid_database_password",
			"the PostgreSQL administrator password must contain 8 to 128 characters",
			"provide a password with upper-case, lower-case, numeric, and non-alphanumeric characters",
		)
	}
	var upper, lower, digit, symbol bool
	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			upper = true
		case unicode.IsLower(char):
			lower = true
		case unicode.IsDigit(char):
			digit = true
		default:
			symbol = true
		}
	}
	if !upper || !lower || !digit || !symbol {
		return validationError(
			"invalid_database_password",
			"the PostgreSQL administrator password does not meet Azure complexity requirements",
			"provide a password with upper-case, lower-case, numeric, and non-alphanumeric characters",
		)
	}
	if strings.ContainsAny(password, `"'\\`) {
		return validationError(
			"invalid_database_password",
			"the PostgreSQL administrator password contains unsupported quote or backslash characters",
			"choose a password without single quotes, double quotes, or backslashes",
		)
	}
	return nil
}

func generatePostgreSQLPassword() (string, error) {
	const (
		upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		lower   = "abcdefghijkmnopqrstuvwxyz"
		digits  = "23456789"
		symbols = "!@#%^*-_=+"
	)
	all := upper + lower + digits + symbols
	password := []byte{upper[0], lower[0], digits[0], symbols[0]}
	for len(password) < generatedPasswordLength {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
		if err != nil {
			return "", fmt.Errorf("generating PostgreSQL password: %w", err)
		}
		password = append(password, all[index.Int64()])
	}
	for index := len(password) - 1; index > 0; index-- {
		swap, err := rand.Int(rand.Reader, big.NewInt(int64(index+1)))
		if err != nil {
			return "", fmt.Errorf("shuffling PostgreSQL password: %w", err)
		}
		password[index], password[swap.Int64()] = password[swap.Int64()], password[index]
	}
	return string(password), nil
}

func resolvePostgreSQLPassword(
	values map[string]string,
	lookupProcess func(string) (string, bool),
	ensureCanGenerate func() error,
) (string, bool, error) {
	persisted, hasPersisted := values[databasePasswordEnv]
	provided, hasProvided := lookupProcess(databasePasswordEnv)

	if hasPersisted {
		if persisted == "" {
			return "", false, validationError(
				"empty_database_password",
				fmt.Sprintf("%s is explicitly empty in the selected azd environment", databasePasswordEnv),
				"unset the empty value or provide a valid PostgreSQL password",
			)
		}
		if err := validatePostgreSQLPassword(persisted); err != nil {
			return "", false, err
		}
		if hasProvided && provided != persisted {
			return "", false, validationError(
				"conflicting_database_password",
				"a supplied PostgreSQL password conflicts with the persisted password",
				"remove the process value or provide the original persisted password; automatic rotation is not supported",
			)
		}
		return persisted, false, nil
	}

	if hasProvided {
		if provided == "" {
			return "", false, validationError(
				"empty_database_password",
				fmt.Sprintf("%s is explicitly empty", databasePasswordEnv),
				"provide a valid PostgreSQL password or remove the variable to generate one for a new deployment",
			)
		}
		if err := validatePostgreSQLPassword(provided); err != nil {
			return "", false, err
		}
		return provided, true, nil
	}

	if values[postgresServerIDEnv] != "" ||
		values[postgresServerNameEnv] != "" ||
		values[provisionedDatabaseTypeEnv] == string(databasePostgreSQL) {
		return "", false, missingExistingDatabasePasswordError()
	}

	if err := ensureCanGenerate(); err != nil {
		return "", false, err
	}

	password, err := generatePostgreSQLPassword()
	return password, true, err
}

func missingExistingDatabasePasswordError() error {
	return validationError(
		"missing_existing_database_password",
		"the selected environment has existing infrastructure but no local PostgreSQL password",
		"restore SPRING_DATABASE_PASSWORD with the original administrator password before retrying",
	)
}
