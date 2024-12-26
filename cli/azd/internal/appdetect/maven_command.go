package appdetect

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func getMvnCommand() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("can not get working directory")
	}
	return getMvnCommandFromPath(cwd)
}

func getMvnCommandFromPath(path string) (string, error) {
	mvnwCommand, err := getMvnwCommand(path)
	if err == nil {
		return mvnwCommand, nil
	}
	if commandExistsInPath("mvn") {
		return "mvn", nil
	}
	return getDownloadedMvnCommand("3.9.9")
}

func getMvnwCommand(path string) (string, error) {
	mvnwCommand := "mvnw"
	fileInfo, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if fileInfo.IsDir() {
		dir = path
	}
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

func mavenZipFileName(mavenVersion string) string {
	return "apache-maven-" + mavenVersion + "-bin.zip"
}

func mavenUrl(mavenVersion string) string {
	return "https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/" +
		mavenVersion + "/" + mavenZipFileName(mavenVersion)
}

func getDownloadedMvnCommand(mavenVersion string) (string, error) {
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
		err = os.MkdirAll(mavenDir, os.ModePerm)
		if err != nil {
			return "", fmt.Errorf("unable to create directory: %w", err)
		}
	}

	mavenZipFilePath := filepath.Join(mavenDir, mavenZipFileName(mavenVersion))
	err = downloadMaven(mavenVersion, mavenZipFilePath)
	if err != nil {
		return "", err
	}
	err = unzip(mavenZipFilePath, mavenDir)
	if err != nil {
		return "", fmt.Errorf("failed to unzip maven bin.zip: %w", err)
	}
	return mavenCommand, nil
}

func getAzdMvnDir() (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to get user home directory: %w", err)
	}
	return filepath.Join(userHome, ".azd", "java", "maven"), nil
}

func getAzdMvnCommand(mavenVersion string) (string, error) {
	mavenDir, err := getAzdMvnDir()
	if err != nil {
		return "", err
	}
	azdMvnCommand := filepath.Join(mavenDir, "apache-maven-"+mavenVersion, "bin", "mvn")
	return azdMvnCommand, nil
}

func downloadMaven(mavenVersion string, filePath string) error {
	requestUrl := mavenUrl(mavenVersion)
	data, err := download(requestUrl)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0600)
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
