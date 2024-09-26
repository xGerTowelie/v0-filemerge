package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "io/fs"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "flag"
)

type Config struct {
    RootFolder        string   `json:"root_folder"`
    OutputFolder      string   `json:"output_folder"`
    MaxFileSizeMB     int      `json:"max_file_size_mb"`
    BlacklistedFolders []string `json:"blacklisted_folders"`
    IgnoredFileTypes  []string `json:"ignored_file_types"`
}

const MB = 1024 * 1024

func main() {
    // Define a flag for the config file path
    configPath := flag.String("config", "config.json", "Path to the configuration file")
    flag.Parse()

    // Get the absolute path of the config file
    absConfigPath, err := filepath.Abs(*configPath)
    if err != nil {
        fmt.Println("Error getting absolute path of config file:", err)
        return
    }

    // Get the directory of the config file
    configDir := filepath.Dir(absConfigPath)

    // Load configuration
    config, err := loadConfig(absConfigPath)
    if err != nil {
        fmt.Println("Error loading config:", err)
        return
    }

    // Resolve paths relative to the config file location
    outputFolder := resolveRelativePath(configDir, config.OutputFolder)
    rootFolder := resolveRelativePath(configDir, expandPath(config.RootFolder))

    fmt.Printf("Config file: %s\n", absConfigPath)
    fmt.Printf("Output folder: %s\n", outputFolder)
    fmt.Printf("Root folder: %s\n", rootFolder)

    // Clean output directory
    err = cleanOutputDirectory(outputFolder)
    if err != nil {
        fmt.Println("Error cleaning output directory:", err)
        return
    }

    // Find Node.js projects (those with package.json) at the top level
    nodeProjects, err := findTopLevelNodeProjects(rootFolder)
    if err != nil {
        fmt.Println("Error scanning projects:", err)
        return
    }

    if len(nodeProjects) == 0 {
        fmt.Println("No Node.js projects found.")
        return
    }

    // Let the user select a project using fzf
    selectedProject, err := selectProjectWithFzf(nodeProjects)
    if err != nil {
        fmt.Println("Error selecting project:", err)
        return
    }

    fmt.Printf("Selected project: %s\n", selectedProject)

    // Process the selected project
    outputFileIndex := 1
    currentFileSize := 0
    var outputFile *os.File

    err = filepath.Walk(selectedProject, func(path string, info fs.FileInfo, err error) error {
        if err != nil {
            return err
        }

        // Skip blacklisted folders
        if info.IsDir() && isBlacklisted(path, config.BlacklistedFolders) {
            return filepath.SkipDir
        }

        // Ignore files in the root directory of the selected project
        if !info.IsDir() && isInRoot(selectedProject, path) {
            return nil // Skip this file
        }

        // Ignore files with specific extensions (e.g., binaries)
        if !info.IsDir() && hasIgnoredExtension(path, config.IgnoredFileTypes) {
            return nil // Skip this file type
        }

        // Process only files in subdirectories
        if !info.IsDir() {
            content, err := os.ReadFile(path)
            if err != nil {
                return err
            }

            // Ensure output file exists and doesn't exceed the max size
            if outputFile == nil || currentFileSize+len(content) > config.MaxFileSizeMB*MB {
                if outputFile != nil {
                    outputFile.Close()
                }
                outputFile, err = createNewOutputFile(outputFolder, outputFileIndex)
                if err != nil {
                    return err
                }
                outputFileIndex++
                currentFileSize = 0
            }

            // Write file path as a comment and append the content
            relPath, _ := filepath.Rel(selectedProject, path)
            writeFileWithComment(outputFile, relPath, content)
            currentFileSize += len(content)
        }
        return nil
    })

    if err != nil {
        fmt.Println("Error processing project:", err)
    }

    if outputFile != nil {
        outputFile.Close()
    }

    fmt.Println("Merging complete.")
}

func loadConfig(configPath string) (Config, error) {
    content, err := os.ReadFile(configPath)
    if err != nil {
        return Config{}, err
    }

    var config Config
    err = json.Unmarshal(content, &config)
    if err != nil {
        return Config{}, err
    }

    return config, nil
}

func expandPath(path string) string {
    if strings.HasPrefix(path, "~") {
        homeDir, _ := os.UserHomeDir()
        return filepath.Join(homeDir, path[1:])
    }
    return path
}

func cleanOutputDirectory(outputDir string) error {
    // Remove the entire output directory and its contents
    err := os.RemoveAll(outputDir)
    if err != nil && !os.IsNotExist(err) {
        return err
    }

    // Recreate the output directory
    return os.MkdirAll(outputDir, os.ModePerm)
}

func findTopLevelNodeProjects(rootFolder string) ([]string, error) {
    var projects []string
    entries, err := os.ReadDir(rootFolder)
    if err != nil {
        return projects, err
    }

    for _, entry := range entries {
        if entry.IsDir() {
            packagePath := filepath.Join(rootFolder, entry.Name(), "package.json")
            if _, err := os.Stat(packagePath); err == nil {
                projects = append(projects, filepath.Join(rootFolder, entry.Name()))
            }
        }
    }

    return projects, nil
}

func isBlacklisted(path string, blacklistedFolders []string) bool {
    for _, folder := range blacklistedFolders {
        if strings.Contains(path, folder) {
            return true
        }
    }
    return false
}

func createNewOutputFile(outputDir string, index int) (*os.File, error) {
    fileName := fmt.Sprintf("%d.txt", index)
    outputPath := filepath.Join(outputDir, fileName)
    return os.Create(outputPath)
}

func writeFileWithComment(outputFile *os.File, relPath string, content []byte) {
    if startsWithComment(content) {
        fmt.Printf("Warning: The file %s starts with a comment.\n", relPath)
    }

    writer := bufio.NewWriter(outputFile)
    writer.WriteString("// " + relPath + "\n")
    writer.Write(content)
    writer.WriteString("\n\n")
    writer.Flush()
}

func selectProjectWithFzf(projects []string) (string, error) {
    cmd := exec.Command("fzf")

    stdin, err := cmd.StdinPipe()
    if err != nil {
        return "", err
    }

    go func() {
        defer stdin.Close()
        for _, project := range projects {
            fmt.Fprintln(stdin, project)
        }
    }()

    output, err := cmd.Output()
    if err != nil {
        return "", err
    }

    return strings.TrimSpace(string(output)), nil
}

func isInRoot(rootFolder string, filePath string) bool {
    relativePath, err := filepath.Rel(rootFolder, filePath)
    if err != nil {
        return false
    }

    return !strings.Contains(relativePath, string(os.PathSeparator))
}

func hasIgnoredExtension(filePath string, ignoredExtensions []string) bool {
    for _, ext := range ignoredExtensions {
        if strings.HasSuffix(filePath, ext) {
            return true
        }
    }
    return false
}

func startsWithComment(content []byte) bool {
    trimmedContent := strings.TrimSpace(string(content))
    return strings.HasPrefix(trimmedContent, "//") || strings.HasPrefix(trimmedContent, "/*") || strings.HasPrefix(trimmedContent, "#")
}

func resolveRelativePath(basePath, relativePath string) string {
    if filepath.IsAbs(relativePath) {
        return relativePath
    }
    return filepath.Join(basePath, relativePath)
}
