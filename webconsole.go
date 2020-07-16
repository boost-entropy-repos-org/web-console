package main

/*
Web Console - lets end-users run command-line applications via a web page, complete with authentication and a user interface.
Acts as its own self-contained web server.
*/

import (
	// Standard libraries.
	"fmt"
	"os"
	"log"
	"time"
	"bufio"
	"strings"
	"os/exec"
	"math/rand"
	"io/ioutil"
	"net/http"
)

// Characters to use to generate new ID strings. Lowercase only - any user-provided IDs will be lowercased before use.
const letters = "abcdefghijklmnopqrstuvwxyz1234567890"

// The timeout, in seconds, of token validity.
const tokenTimeout = 600
// How often, in seconds, to check token for expired tokens.
const tokenCheckPeriod = 60

// Set up the tokens map.
var tokens = map[string]int64{}

var runningTasks = map[string]*exec.Cmd{}
var taskOutputs = map[string][]byte{}

// Generate a new, random 16-character ID.
func generateIDString() string {
	rand.Seed(time.Now().UnixNano())
	result := make([]byte, 16)
	for pl := range result {
		result[pl] = letters[rand.Intn(len(letters))]
	}
	return string(result)
}

// Clear any expired tokens from memory.
func clearExpiredTokens() {
	// This is a periodic task, it runs in a separate thread.
	for true {
		currentTimestamp := time.Now().Unix()
		for token, timestamp := range tokens { 
			if currentTimestamp - tokenTimeout > timestamp {
				delete(tokens, token)
			}
		}
		time.Sleep(tokenCheckPeriod * time.Second)
	}
}

// Split a string representing a command line with paramaters, possibly with quoted sections, into an array of strings.
func parseCommandString(theString string) []string {
	var result []string
	var stringSplit []string
	for theString != "" {
		fmt.Printf(theString + "\n")
		theString = strings.TrimSpace(theString)
		if strings.HasPrefix(theString, "\"") {
			stringSplit = strings.SplitN(theString[1:], "\"", 2)
		} else {
			stringSplit = strings.SplitN(theString, " ", 2)
		}
		result = append(result, stringSplit[0])
		if len(stringSplit) > 0 {
			theString = stringSplit[1]
		} else {
			theString = ""
		}
	}
	fmt.Printf("endOfParseCommandString\n")
	return result
}

// The main body of the program - parse user-provided command-line paramaters, or start the main web server process.
func main() {
	// Start the thread that checks for and clears expired tokens.
	go clearExpiredTokens()
	if len(os.Args) == 1 {
		// If no parameters are given, simply start the web server.
		fmt.Println("Starting web server...")
		
		// We write our own function to parse the request URL.
		http.HandleFunc("/", func (theResponseWriter http.ResponseWriter, theRequest *http.Request) {
			// Make sure submitted form values are parsed.
			theRequest.ParseForm()
			
			// The default root - serve index.html.
			if theRequest.URL.Path == "/" {
				http.ServeFile(theResponseWriter, theRequest, "www/index.html")
			// Handle a View Task or API request. taskID needs to be provided as a parameter, either via GET or POST.
			} else if strings.HasPrefix(theRequest.URL.Path, "/view") || strings.HasPrefix(theRequest.URL.Path, "/api/") {
				taskID := theRequest.Form.Get("taskID")
				token := theRequest.Form.Get("token")
				if taskID == "" {
					fmt.Fprintf(theResponseWriter, "ERROR: Missing parameter taskID.")
				} else {
					configPath := "tasks/" + taskID + "/config.txt"
					// Check to see if we have a valid task ID.
					if _, err := os.Stat(configPath); !os.IsNotExist(err) {
						inFile, inFileErr := os.Open(configPath)
						if inFileErr != nil {
							fmt.Fprintf(theResponseWriter, "ERROR: Can't open Task config file.")
						} else {
							// Read the Task's details from its config file.
							taskDetails := make(map[string]string)
							taskDetails["title"] = ""
							taskDetails["secret"] = ""
							taskDetails["command"] = ""							
							scanner := bufio.NewScanner(inFile)
							for scanner.Scan() {
								itemSplit := strings.SplitN(scanner.Text(), ":", 2)
								taskDetails[strings.TrimSpace(itemSplit[0])] = strings.TrimSpace(itemSplit[1])
							}
							inFile.Close()
							
							authorised := false
							authorisationError := "unknown error"
							currentTimestamp := time.Now().Unix()
							if token != "" {
								if tokens[token] == 0 {
									authorisationError = "invalid or expired token"
								} else {
									authorised = true
								}
							} else if theRequest.Form.Get("secret") == taskDetails["secret"] {								
								authorised = true
							} else {
								authorisationError = "incorrect secret"
							}
							if authorised {
								if token == "" {
									token = generateIDString()
								}
								tokens[token] = currentTimestamp
								// Handle View Task requests.
								if strings.HasPrefix(theRequest.URL.Path, "/view") {
									// Serve the webconsole.html file, first adding in the Task ID value so it can be used client-side.
									webconsoleBuffer, fileReadErr := ioutil.ReadFile("www/webconsole.html")
									if fileReadErr == nil {
										webconsoleString := string(webconsoleBuffer)
										webconsoleString = strings.Replace(webconsoleString, "taskID = \"\"", "taskID = \"" + taskID + "\"", -1)
										webconsoleString = strings.Replace(webconsoleString, "token = \"\"", "token = \"" + token + "\"", -1)
										http.ServeContent(theResponseWriter, theRequest, "webconsole.html", time.Now(), strings.NewReader(webconsoleString))
									} else {
										authorisationError = "couldn't read webconsole.html"
									}
								// API - Exchange the secret for a token.
								} else if strings.HasPrefix(theRequest.URL.Path, "/api/getToken") {
									fmt.Fprintf(theResponseWriter, token)
								// API - Return the Task's title.
								} else if strings.HasPrefix(theRequest.URL.Path, "/api/getTaskTitle") {
									fmt.Fprintf(theResponseWriter, taskDetails["title"])
								// API - Run a given Task.
								} else if strings.HasPrefix(theRequest.URL.Path, "/api/runTask") {
									commandArray := parseCommandString(taskDetails["command"])
									//commandArray := []string{"C:\\Windows\\System32\\cmd.exe", "/C", "dir"}
									var commandArgs []string
									if len(commandArray) > 0 {
										commandArgs = commandArray[1:]
									}
									fmt.Printf("Running: " + commandArray[0] + "\n")
									//fmt.Printf("   Args: " + commandArgs + "\n")
									runningTasks[taskID] = exec.Command(commandArray[0], commandArgs...)
									taskOutput, taskErr := runningTasks[taskID].CombinedOutput()
									if taskErr == nil {
										taskOutputs[taskID] = taskOutput
										fmt.Fprintf(theResponseWriter, "OK")
									} else {
										fmt.Printf("ERROR: " + taskErr.Error())
										fmt.Fprintf(theResponseWriter, "ERROR: " + taskErr.Error())
									}
								} else if strings.HasPrefix(theRequest.URL.Path, "/api/getJobOutput") {
									fmt.Printf("Called getJobOutput...\n")
									fmt.Printf(string(taskOutputs[taskID]))
									fmt.Fprintf(theResponseWriter, string(taskOutputs[taskID]))
								} else if strings.HasPrefix(theRequest.URL.Path, "/api/") {
									fmt.Fprintf(theResponseWriter, "ERROR: Unknown API call: %s", theRequest.URL.Path)
								}
							} else {
								fmt.Fprintf(theResponseWriter, "ERROR: Not authorised - %s.", authorisationError)
							}
						}
					} else {
						fmt.Fprintf(theResponseWriter, "ERROR: Invalid taskID.")
					}
				}
			// Otherwise, try and find the static file referred to by the request URL.
			} else {
				http.ServeFile(theResponseWriter, theRequest, "www" + theRequest.URL.Path)
			}
		})
		log.Fatal(http.ListenAndServe(":8090", nil))
	} else if os.Args[1] == "-list" {
		// Print a list of existing IDs.
		items, err := ioutil.ReadDir("tasks")
		if err != nil {
			log.Fatal(err)
		}
		for _, item := range items {
			fmt.Println(item.Name())
		}
	} else if os.Args[1] == "-generate" {
		// Generate a new task ID, and create a matching folder.
		for {
			newTaskID := generateIDString()
			if _, err := os.Stat("tasks/" + newTaskID); os.IsNotExist(err) {
				os.Mkdir("tasks/" + newTaskID, os.ModePerm)
				fmt.Println("New Task generated: " + newTaskID)
				break
			}
		}
	}
}
