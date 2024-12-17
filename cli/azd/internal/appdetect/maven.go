package appdetect

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func getMvnCommand(pomPath string) (string, error) {
	mvnwCommand, err := getMvnwCommandInProject(pomPath)
	if err == nil {
		return mvnwCommand, nil
	}
	if commandExistsInPath("mvn") {
		return "mvn", nil
	}
	return getDownloadedMvnCommand()
}

func getMvnwCommandInProject(pomPath string) (string, error) {
	mvnwCommand := "mvnw"
	dir := filepath.Dir(pomPath)
	for {
		commandPath := filepath.Join(dir, mvnwCommand)
		if fileExists(commandPath) {
			return commandPath, nil
		}
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			break
		}
		dir = parentDir
	}
	return "", fmt.Errorf("failed to find mvnw command in project")
}

const mavenVersion = "3.9.9"
const mavenURL = "https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/" +
	mavenVersion + "/apache-maven-" + mavenVersion + "-bin.zip"

func getDownloadedMvnCommand() (string, error) {
	mavenCommand, err := getAzdMvnCommand(mavenVersion)
	if err != nil {
		return "", err
	}
	if fileExists(mavenCommand) {
		log.Println("Skip downloading maven because it already exists.")
		return mavenCommand, nil
	}
	log.Println("Downloading maven")
	mavenDir, err := getAzdMvnDir()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(mavenDir); os.IsNotExist(err) {
		err = os.Mkdir(mavenDir, os.ModePerm)
		if err != nil {
			return "", fmt.Errorf("unable to create directory: %w", err)
		}
	}

	mavenFile := fmt.Sprintf("maven-wrapper-%s-bin.zip", mavenVersion)
	wrapperPath := filepath.Join(mavenDir, mavenFile)
	err = downloadMaven(wrapperPath)
	if err != nil {
		return "", err
	}
	err = unzip(wrapperPath, mavenDir)
	if err != nil {
		return "", fmt.Errorf("failed to unzip maven bin.zip: %w", err)
	}
	return mavenCommand, nil
}

func getAzdMvnDir() (string, error) {
	azdMvnFolderName := "azd-maven"
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to get user home directory: %w", err)
	}
	return filepath.Join(userHome, azdMvnFolderName), nil
}

func getAzdMvnCommand(mavenVersion string) (string, error) {
	mavenDir, err := getAzdMvnDir()
	if err != nil {
		return "", err
	}
	azdMvnCommand := filepath.Join(mavenDir, "apache-maven-"+mavenVersion, "bin", "mvn")
	return azdMvnCommand, nil
}

func downloadMaven(filepath string) error {
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer func(out *os.File) {
		err := out.Close()
		if err != nil {
			log.Println("failed to close file. %w", err)
		}
	}(out)

	resp, err := http.Get(mavenURL)
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Println("failed to close ReadCloser. %w", err)
		}
	}(resp.Body)

	_, err = io.Copy(out, resp.Body)
	return err
}

func unzip(src string, destinationFolder string) error {
	reader, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func(reader *zip.ReadCloser) {
		err := reader.Close()
		if err != nil {
			log.Println("failed to close ReadCloser. %w", err)
		}
	}(reader)

	for _, file := range reader.File {
		destinationPath, err := getValidDestPath(destinationFolder, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			err := os.MkdirAll(destinationPath, os.ModePerm)
			if err != nil {
				return err
			}
		} else {
			if err = os.MkdirAll(filepath.Dir(destinationPath), os.ModePerm); err != nil {
				return err
			}

			outFile, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
			if err != nil {
				return err
			}
			defer func(outFile *os.File) {
				err := outFile.Close()
				if err != nil {
					log.Println("failed to close file. %w", err)
				}
			}(outFile)

			rc, err := file.Open()
			if err != nil {
				return err
			}
			defer func(rc io.ReadCloser) {
				err := rc.Close()
				if err != nil {
					log.Println("failed to close file. %w", err)
				}
			}(rc)

			for {
				_, err = io.CopyN(outFile, rc, 1_000_000)
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return err
				}
			}
		}
	}
	return nil
}

func getValidDestPath(destinationFolder string, fileName string) (string, error) {
	destinationPath := filepath.Clean(filepath.Join(destinationFolder, fileName))
	if !strings.HasPrefix(destinationPath, destinationFolder+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s: illegal file path", fileName)
	}
	return destinationPath, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	} else {
		return false
	}
}
