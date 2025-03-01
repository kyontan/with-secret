package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
)

func PrintUsageAndExit() {
	fmt.Println("Usage: with-secret command")
	fmt.Println("  command: command to execute")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  WITH_SECRET_ID: AWS Secrets Manager ID")
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  $ export WITH_SECRET_ID=my-secret-id")
	fmt.Println("  $ with-secret my-command")
	os.Exit(1)
}

func GetSecretFromAWS(secret_id string) string {
	return GetSecretWithVersionIdFromAWS(secret_id, "AWSCURRENT")
}

func GetSecretWithVersionIdFromAWS(secret_id, version_id string) string {
	// get secret from AWS Secrets Manager

	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secret_id),
	}

	if version_id != "AWSCURRENT" {
		input.VersionId = aws.String(version_id)
	}

	sess := session.Must(session.NewSession())
	svc := secretsmanager.New(sess)

	result, err := svc.GetSecretValue(input)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	return *result.SecretString
}

func main() {
	// check if WITH_SECRET_ID is set
	secret_id := os.Getenv("WITH_SECRET_ID")

	if secret_id == "" {
		fmt.Fprintln(os.Stderr, "[with-secret] Error: WITH_SECRET_ID is not set")
		PrintUsageAndExit()
	}

	// check if command is provided
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "[with-secret] Error: command is not provided")
		PrintUsageAndExit()
	}

	// get secret from AWS Secrets Manager
	secret_version_id := os.Getenv("WITH_SECRETS_VERSION_ID")
	var secret string
	if secret_version_id == "" {
		secret = GetSecretFromAWS(secret_id)
	} else {
		secret = GetSecretWithVersionIdFromAWS(secret_id, secret_version_id)
	}

	// assumption: secret is JSON and has key-value pairs
	// parse secret JSON
	secret_json := []byte(secret)
	secret_map_original := make(map[string]interface{}) // value might be string or integer or ...
	err := json.Unmarshal(secret_json, &secret_map_original)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[with-secret] Error: secret is not JSON or not key-value pairs: %+v\n", err)
		os.Exit(1)
	}

	secret_map := make(map[string]string)
	for key, value := range secret_map_original {
		secret_map[key] = fmt.Sprintf("%v", value)
	}

	// Build the trie-tree from secret_map values
	trie := BuildTrieFromSecretsMap(secret_map)
	// trie := NewTrieTree() // no mask

	// execute command with secret
	cmd := os.Args[1]
	args := os.Args[2:]

	command := exec.Command(cmd, args...)

	// set environment variables
	command.Env = os.Environ()
	for key, value := range secret_map {
		command.Env = append(command.Env, key+"="+value)
	}

	stdout, err := command.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[with-secret] Error: unable to get stdout pipe: %+v\n", err)
		os.Exit(1)
	}

	stderr, err := command.StderrPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[with-secret] Error: unable to get stderr pipe: %+v\n", err)
		os.Exit(1)
	}

	if err := command.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[with-secret] Error: unable to start command: %+v\n", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Split(bufio.ScanRunes)
		trie_state := NewTrieNodeState()
		var remaining string
		for scanner.Scan() {
			var masked string
			line := scanner.Text()
			remaining += line
			masked, remaining, trie_state = trie.Mask(remaining, trie_state)
			fmt.Print(masked)
		}
		masked := trie.PrintRemaining(remaining, trie_state)
		fmt.Print(masked)
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Split(bufio.ScanRunes)

		trie_state := NewTrieNodeState()
		var remaining string
		for scanner.Scan() {
			var masked string
			line := scanner.Text()
			remaining += line
			masked, remaining, trie_state = trie.Mask(remaining, trie_state)
			fmt.Print(masked)
		}
		masked := trie.PrintRemaining(remaining, trie_state)
		fmt.Print(masked)
	}()

	wg.Wait()

	if err := command.Wait(); err != nil {
		fmt.Fprintf(os.Stderr, "[with-secret] Error: command execution failed: %+v\n", err)
		os.Exit(1)
	}
}
