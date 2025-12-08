// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real-world LLM workflow integration tests
// These simulate actual scenarios where LLMs read multiple sections,
// make edits via WriteFileTool, then read again to verify changes
func TestReadFileTool_LLMWorkflow_CodeAnalysisAndEdit(t *testing.T) {
	// Create test tools with security manager
	tools, tempDir := createTestTools(t)
	readTool := tools["read"].(ReadFileTool)
	writeTool := tools["write"].(WriteFileTool)

	testFile := filepath.Join(tempDir, "calculator.go")

	// Simple initial Go code
	initialContent := `package main

import "fmt"

func add(a, b int) int {
	return a + b
}

func main() {
	result := add(5, 3)
	fmt.Println(result)
}`

	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	// Step 1: LLM reads the entire file to understand structure
	readRequest1 := ReadFileRequest{
		Path: testFile,
	}
	result1, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest1))
	assert.NoError(t, err)

	var response1 ReadFileResponse
	err = json.Unmarshal([]byte(result1), &response1)
	require.NoError(t, err)
	assert.True(t, response1.Success)
	assert.Contains(t, response1.Content, "func add")
	assert.Contains(t, response1.Content, "func main")

	// Step 2: LLM reads just the add function (lines 5-7)
	readRequest2 := ReadFileRequest{
		Path:      testFile,
		StartLine: 5,
		EndLine:   7,
	}
	result2, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest2))
	assert.NoError(t, err)

	var response2 ReadFileResponse
	err = json.Unmarshal([]byte(result2), &response2)
	require.NoError(t, err)
	assert.True(t, response2.Success)
	assert.Contains(t, response2.Content, "func add(a, b int) int")
	assert.Equal(t, 3, response2.LineRange.LinesRead)

	// Step 3: LLM replaces the add function with a more robust version
	newFunction := `func add(a, b int) int {
	fmt.Printf("Adding %d + %d\n", a, b)
	return a + b
}`

	writeRequest := WriteFileRequest{
		Path:      testFile,
		Content:   newFunction,
		StartLine: 5,
		EndLine:   7,
	}
	writeResult, err := writeTool.Call(context.Background(), mustMarshalJSON(writeRequest))
	assert.NoError(t, err)
	assert.Contains(t, writeResult, `"success": true`)

	// Step 4: LLM reads the updated function to verify change
	readRequest3 := ReadFileRequest{
		Path:      testFile,
		StartLine: 5,
		EndLine:   8,
	}
	result3, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest3))
	assert.NoError(t, err)

	var response3 ReadFileResponse
	err = json.Unmarshal([]byte(result3), &response3)
	require.NoError(t, err)
	assert.True(t, response3.Success)
	assert.Contains(t, response3.Content, "Printf")
	assert.Contains(t, response3.Content, "Adding %d + %d")

	// Step 5: LLM reads main function (which may have shifted)
	readRequest4 := ReadFileRequest{
		Path:      testFile,
		StartLine: 9,
		EndLine:   12,
	}
	result4, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest4))
	assert.NoError(t, err)

	var response4 ReadFileResponse
	err = json.Unmarshal([]byte(result4), &response4)
	require.NoError(t, err)
	assert.True(t, response4.Success)
	assert.Contains(t, response4.Content, "func main")
}

func TestReadFileTool_LLMWorkflow_MultiplePartialReadsAndWrites(t *testing.T) {
	// Create test tools with security manager
	tools, tempDir := createTestTools(t)
	readTool := tools["read"].(ReadFileTool)
	writeTool := tools["write"].(WriteFileTool)

	configFile := filepath.Join(tempDir, "config.yaml")

	initialConfig := `# Application Configuration
app:
  name: "MyApp"
  version: "1.0.0"
  debug: false

database:
  host: "localhost"
  port: 5432
  name: "myapp_db"
  ssl: false

redis:
  host: "localhost"
  port: 6379
  db: 0

logging:
  level: "info"
  format: "json"
  output: "stdout"
`

	err := os.WriteFile(configFile, []byte(initialConfig), 0600)
	require.NoError(t, err)

	// Step 1: LLM scans file structure (first 10 lines)
	readRequest1 := ReadFileRequest{
		Path:      configFile,
		StartLine: 1,
		EndLine:   10,
	}
	result1, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest1))
	assert.NoError(t, err)

	var response1 ReadFileResponse
	err = json.Unmarshal([]byte(result1), &response1)
	require.NoError(t, err)
	assert.True(t, response1.Success)
	assert.Contains(t, response1.Content, "app:")
	assert.Contains(t, response1.Content, "database:")

	// Step 2: LLM focuses on database section
	readRequest2 := ReadFileRequest{
		Path:      configFile,
		StartLine: 7,
		EndLine:   12,
	}
	result2, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest2))
	assert.NoError(t, err)

	var response2 ReadFileResponse
	err = json.Unmarshal([]byte(result2), &response2)
	require.NoError(t, err)
	assert.True(t, response2.Success)
	assert.Contains(t, response2.Content, "host: \"localhost\"")
	assert.Contains(t, response2.Content, "ssl: false")

	// Step 3: LLM updates database config for production
	newDbConfig := `database:
  host: "prod-db.example.com"
  port: 5432
  name: "myapp_production"
  ssl: true
  pool_size: 20`

	writeRequest1 := WriteFileRequest{
		Path:      configFile,
		Content:   newDbConfig,
		StartLine: 7,
		EndLine:   11,
	}
	writeResult1, err := writeTool.Call(context.Background(), mustMarshalJSON(writeRequest1))
	assert.NoError(t, err)
	assert.Contains(t, writeResult1, `"success": true`)

	// Step 4: LLM reads redis section (which should have moved due to previous edit)
	readRequest3 := ReadFileRequest{
		Path:      configFile,
		StartLine: 13,
		EndLine:   16,
	}
	result3, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest3))
	assert.NoError(t, err)

	var response3 ReadFileResponse
	err = json.Unmarshal([]byte(result3), &response3)
	require.NoError(t, err)
	assert.True(t, response3.Success)
	assert.Contains(t, response3.Content, "redis:")

	// Step 5: LLM reads logging section to update it
	readRequest4 := ReadFileRequest{
		Path:      configFile,
		StartLine: 17,
		EndLine:   21,
	}
	result4, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest4))
	assert.NoError(t, err)

	var response4 ReadFileResponse
	err = json.Unmarshal([]byte(result4), &response4)
	require.NoError(t, err)
	assert.True(t, response4.Success)
	assert.Contains(t, response4.Content, "logging:")

	// Step 6: LLM updates logging for production
	newLoggingConfig := `logging:
  level: "warn"
  format: "structured"
  output: "file"
  file: "/var/log/myapp.log"
  rotation: "daily"`

	writeRequest2 := WriteFileRequest{
		Path:      configFile,
		Content:   newLoggingConfig,
		StartLine: 17,
		EndLine:   20,
	}
	writeResult2, err := writeTool.Call(context.Background(), mustMarshalJSON(writeRequest2))
	assert.NoError(t, err)
	assert.Contains(t, writeResult2, `"success": true`)

	// Step 7: LLM does final validation read of entire file
	readRequestFinal := ReadFileRequest{
		Path: configFile,
	}
	resultFinal, err := readTool.Call(context.Background(), mustMarshalJSON(readRequestFinal))
	assert.NoError(t, err)

	var responseFinal ReadFileResponse
	err = json.Unmarshal([]byte(resultFinal), &responseFinal)
	require.NoError(t, err)
	assert.True(t, responseFinal.Success)
	assert.Contains(t, responseFinal.Content, "prod-db.example.com")
	assert.Contains(t, responseFinal.Content, "ssl: true")
	assert.Contains(t, responseFinal.Content, "level: \"warn\"")
	assert.Contains(t, responseFinal.Content, "rotation: \"daily\"")
}

func TestReadFileTool_LLMWorkflow_RefactoringWithContext(t *testing.T) {
	// Create test tools with security manager
	tools, tempDir := createTestTools(t)
	readTool := tools["read"].(ReadFileTool)
	writeTool := tools["write"].(WriteFileTool)

	classFile := filepath.Join(tempDir, "user_service.py")

	initialPython := `"""User service for managing user operations."""

import logging
from typing import Optional, List
from database import Database

class UserService:
    """Service class for user management."""
    
    def __init__(self, db: Database):
        self.db = db
        self.logger = logging.getLogger(__name__)
    
    def create_user(self, username: str, email: str) -> bool:
        """Create a new user."""
        try:
            self.logger.info(f"Creating user: {username}")
            query = "INSERT INTO users (username, email) VALUES (?, ?)"
            self.db.execute(query, (username, email))
            return True
        except Exception as e:
            self.logger.error(f"Failed to create user: {e}")
            return False
    
    def get_user(self, user_id: int) -> Optional[dict]:
        """Get user by ID."""
        try:
            query = "SELECT * FROM users WHERE id = ?"
            result = self.db.fetch_one(query, (user_id,))
            return result
        except Exception as e:
            self.logger.error(f"Failed to get user: {e}")
            return None
    
    def delete_user(self, user_id: int) -> bool:
        """Delete user by ID."""
        try:
            query = "DELETE FROM users WHERE id = ?"
            self.db.execute(query, (user_id,))
            return True
        except Exception as e:
            self.logger.error(f"Failed to delete user: {e}")
            return False
`

	err := os.WriteFile(classFile, []byte(initialPython), 0600)
	require.NoError(t, err)

	// Step 1: LLM reads class definition and constructor
	readRequest1 := ReadFileRequest{
		Path:      classFile,
		StartLine: 7,
		EndLine:   12,
	}
	result1, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest1))
	assert.NoError(t, err)

	var response1 ReadFileResponse
	err = json.Unmarshal([]byte(result1), &response1)
	require.NoError(t, err)
	assert.True(t, response1.Success)
	assert.Contains(t, response1.Content, "class UserService:")
	assert.Contains(t, response1.Content, "__init__")

	// Step 2: LLM reads create_user method with some context
	readRequest2 := ReadFileRequest{
		Path:      classFile,
		StartLine: 14,
		EndLine:   22,
	}
	result2, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest2))
	assert.NoError(t, err)

	var response2 ReadFileResponse
	err = json.Unmarshal([]byte(result2), &response2)
	require.NoError(t, err)
	assert.True(t, response2.Success)
	assert.Contains(t, response2.Content, "create_user")
	assert.Contains(t, response2.Content, "INSERT INTO users")

	// Step 3: LLM refactors create_user method to add validation
	improvedCreateUser := `    def create_user(self, username: str, email: str) -> bool:
        """Create a new user with validation."""
        if not username or not email:
            self.logger.warning("Username and email are required")
            return False
            
        if "@" not in email:
            self.logger.warning(f"Invalid email format: {email}")
            return False
            
        try:
            self.logger.info(f"Creating user: {username}")
            query = "INSERT INTO users (username, email) VALUES (?, ?)"
            self.db.execute(query, (username, email))
            return True
        except Exception as e:
            self.logger.error(f"Failed to create user: {e}")
            return False`

	writeRequest1 := WriteFileRequest{
		Path:      classFile,
		Content:   improvedCreateUser,
		StartLine: 14,
		EndLine:   22,
	}
	writeResult1, err := writeTool.Call(context.Background(), mustMarshalJSON(writeRequest1))
	assert.NoError(t, err)
	assert.Contains(t, writeResult1, `"success": true`)

	// Step 4: LLM reads get_user method (line numbers shifted due to edit)
	readRequest3 := ReadFileRequest{
		Path:      classFile,
		StartLine: 31,
		EndLine:   38,
	}
	result3, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest3))
	assert.NoError(t, err)

	var response3 ReadFileResponse
	err = json.Unmarshal([]byte(result3), &response3)
	require.NoError(t, err)
	assert.True(t, response3.Success)
	assert.Contains(t, response3.Content, "get_user")

	// Step 5: LLM reads context around delete_user to understand the pattern
	readRequest4 := ReadFileRequest{
		Path:      classFile,
		StartLine: 40,
		EndLine:   47,
	}
	result4, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest4))
	assert.NoError(t, err)

	var response4 ReadFileResponse
	err = json.Unmarshal([]byte(result4), &response4)
	require.NoError(t, err)
	assert.True(t, response4.Success)
	assert.Contains(t, response4.Content, "delete_user")

	// Step 6: LLM verifies the refactoring by reading the updated create_user method
	readRequest5 := ReadFileRequest{
		Path:      classFile,
		StartLine: 14,
		EndLine:   30,
	}
	result5, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest5))
	assert.NoError(t, err)

	var response5 ReadFileResponse
	err = json.Unmarshal([]byte(result5), &response5)
	require.NoError(t, err)
	assert.True(t, response5.Success)
	assert.Contains(t, response5.Content, "if not username or not email:")
	assert.Contains(t, response5.Content, "Invalid email format")
	assert.True(t, response5.IsPartial)
}

func TestReadFileTool_LLMWorkflow_HandleLineShifts(t *testing.T) {
	// Create test tools with security manager
	tools, tempDir := createTestTools(t)
	readTool := tools["read"].(ReadFileTool)
	writeTool := tools["write"].(WriteFileTool)

	testFile := filepath.Join(tempDir, "shifts.txt")

	initialContent := `Line 1
Line 2
Line 3
Line 4
Line 5
Line 6
Line 7
Line 8
Line 9
Line 10`

	err := os.WriteFile(testFile, []byte(initialContent), 0600)
	require.NoError(t, err)

	// Step 1: Read lines 3-5
	readRequest1 := ReadFileRequest{
		Path:      testFile,
		StartLine: 3,
		EndLine:   5,
	}
	result1, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest1))
	assert.NoError(t, err)

	var response1 ReadFileResponse
	err = json.Unmarshal([]byte(result1), &response1)
	require.NoError(t, err)
	assert.True(t, response1.Success)
	assert.Equal(t, "Line 3\nLine 4\nLine 5", response1.Content)

	// Step 2: Insert multiple lines at line 4, shifting everything down
	insertContent := `Line 3
New Line A
New Line B  
New Line C
Line 4`

	writeRequest := WriteFileRequest{
		Path:      testFile,
		Content:   insertContent,
		StartLine: 3,
		EndLine:   4,
	}
	writeResult, err := writeTool.Call(context.Background(), mustMarshalJSON(writeRequest))
	assert.NoError(t, err)
	assert.Contains(t, writeResult, `"success": true`)

	// Step 3: Try to read what was originally line 5 (now line 8)
	readRequest2 := ReadFileRequest{
		Path:      testFile,
		StartLine: 8,
		EndLine:   8,
	}
	result2, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest2))
	assert.NoError(t, err)

	var response2 ReadFileResponse
	err = json.Unmarshal([]byte(result2), &response2)
	require.NoError(t, err)
	assert.True(t, response2.Success)
	assert.Equal(t, "Line 5", response2.Content)

	// Step 4: Read the new inserted content
	readRequest3 := ReadFileRequest{
		Path:      testFile,
		StartLine: 4,
		EndLine:   6,
	}
	result3, err := readTool.Call(context.Background(), mustMarshalJSON(readRequest3))
	assert.NoError(t, err)

	var response3 ReadFileResponse
	err = json.Unmarshal([]byte(result3), &response3)
	require.NoError(t, err)
	assert.True(t, response3.Success)
	assert.Contains(t, response3.Content, "New Line A")
	assert.Contains(t, response3.Content, "New Line B")
	assert.Contains(t, response3.Content, "New Line C")

	// Step 5: Verify total line count changed correctly
	readRequestFull := ReadFileRequest{
		Path: testFile,
	}
	resultFull, err := readTool.Call(context.Background(), mustMarshalJSON(readRequestFull))
	assert.NoError(t, err)

	var responseFull ReadFileResponse
	err = json.Unmarshal([]byte(resultFull), &responseFull)
	require.NoError(t, err)
	assert.True(t, responseFull.Success)
	assert.Contains(t, responseFull.Message, "13 lines") // Originally 10, added 3, removed 1
}
