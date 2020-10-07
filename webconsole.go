package main
// Web Console - lets end-users run command-line applications via a web page, complete with authentication and a user interface.
// For more details, see project page at: https://github.com/dhicks6345789/web-console

import (
	// Standard libraries.
	"io"
	"fmt"
	"os"
	"log"
	"sort"
	"time"
	"bufio"
	"errors"
	"strings"
	"strconv"
	"os/exec"
	"net/http"
	"math/rand"
	"io/ioutil"
	"encoding/csv"
	
	// Bcrypt for password hashing.
	"golang.org/x/crypto/bcrypt"
	
	// Excelize for loading in Excel files.
	"github.com/360EntSecGroup-Skylar/excelize"
)

// Characters to use to generate new ID strings. Lowercase only - any user-provided IDs will be lowercased before use.
const letters = "abcdefghijklmnopqrstuvwxyz1234567890"

// A map to store any arguments passed on the command line.
var arguments = map[string]string{}

// We use tokens for session management, not cookies.
// The timeout, in seconds, of token validity.
const tokenTimeout = 600
// How often, in seconds, to check for expired tokens.
const tokenCheckPeriod = 60
// A map of current valid tokens.
var tokens = map[string]int64{}

// A list of currently running Tasks.
var runningTasks = map[string]*exec.Cmd{}
// The outputs from Tasks.
var taskOutputs = map[string][]string{}
// We record the start time and an array of recent runtimes for each Task so we can guess at this run's liklely time and print a progress report if wanted.
var taskStartTimes = map[string]int64{}
var taskRunTimes = map[string][]int64{}
var taskRuntimeGuesses = map[string]float64{}
// We record the stop time for each Task so we can implement rate limiting.
var taskStopTimes = map[string]int64{}

// Generate a new, random 16-character string, used for tokens and Task IDs.
func generateRandomString() string {
	rand.Seed(time.Now().UnixNano())
	result := make([]byte, 16)
	for pl := range result {
		result[pl] = letters[rand.Intn(len(letters))]
	}
	return string(result)
}

// Use the Bcrypt hashing algorithm to encode a password string.
func hashPassword(thePassword string) (string, error) {
	bytes, cryptErr := bcrypt.GenerateFromPassword([]byte(thePassword), 14)
	return string(bytes), cryptErr
}

// Check a plaint text password with a Bcrypt-hashed string, returns true if they match.
func checkPasswordHash(thePassword, theHash string) bool {
	if thePassword == "" && theHash == "" {
		return true
	}
	cryptErr := bcrypt.CompareHashAndPassword([]byte(theHash), []byte(thePassword))
	return cryptErr == nil
}

// Clear any expired tokens from memory.
func clearExpiredTokens() {
	// This is a periodic task, it runs in a separate thread (goroutine) - the time period is set by the tokenCheckPeriod constant set at the top of the script.
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
		theString = strings.TrimSpace(theString)
		if strings.HasPrefix(theString, "\"") {
			stringSplit = strings.SplitN(theString[1:], "\"", 2)
		} else {
			stringSplit = strings.SplitN(theString, " ", 2)
		}
		result = append(result, stringSplit[0])
		if len(stringSplit) > 1 {
			theString = stringSplit[1]
		} else {
			theString = ""
		}
	}
	return result
}

// Runs a task, capturing output from stdout and placing it in a buffer. Designed to be run as a goroutine, so a task can be run in the background
// and output captured while the user does other stuff.
func runTask(theTaskID string) {
	readBuffer := make([]byte, 10240)
	taskOutputs[theTaskID] = make([]string, 0)
	taskOutput, taskErr := runningTasks[theTaskID].StdoutPipe()
	if taskErr == nil {
		taskErr = runningTasks[theTaskID].Start()
		if taskErr == nil {
			taskRunning := true
			// Loop until the Task (an external executable) has finished.
			for taskRunning {
				readSize, readErr := taskOutput.Read(readBuffer)
				if readErr == nil {
					bufferSplit := strings.Split(string(readBuffer[0:readSize]), "\n")
					for pl := 0; pl < len(bufferSplit); pl++ {
						if strings.TrimSpace(bufferSplit[pl]) != "" {
							taskOutputs[theTaskID] = append(taskOutputs[theTaskID], bufferSplit[pl])
						}
					}
				} else {
					taskRunning = false
				}
			}
			// When we get here, the Task has finished running. We record the finish time and work out the total run time for this run
			// and update (or create) the list of recent run times for this Task.
			taskStopTimes[theTaskID] = time.Now().Unix()
			runTime := taskStopTimes[theTaskID] - taskStartTimes[theTaskID]
			taskRunTimes[theTaskID] = append(taskRunTimes[theTaskID], runTime)
			// We don't just record every runtime, we sort the times and trim them to a set of 10 at most, that way we get a reasonable
			// guess at an average run time, assuming run times are similar each time.
			sort.Slice(taskRunTimes[theTaskID], func(i, j int) bool { return taskRunTimes[theTaskID][i] < taskRunTimes[theTaskID][j] })
			for len(taskRunTimes[theTaskID]) >= 10 {
				taskRunTimes[theTaskID] = taskRunTimes[theTaskID][1:len(taskRunTimes[theTaskID])-2]
			}
			// Write the runTimes.txt file for this Task.
			outputString := ""
			for pl := 0; pl < len(taskRunTimes[theTaskID]); pl = pl + 1 {
				outputString = outputString + strconv.FormatInt(taskRunTimes[theTaskID][pl], 10)
				if pl < len(taskRunTimes[theTaskID])-1 {
					outputString = outputString + "\n"
				}
			}
			ioutil.WriteFile("tasks/" + theTaskID + "/runTimes.txt", []byte(outputString), 0644)
			// Remove this Task from the runnings Tasks list. We don't remove the output right away - client-side code might
			// still not have received all the output yet.
			delete(runningTasks, theTaskID)
		}
	}
}

// Returns true if the given Task is currently running, false otherwise.
func taskIsRunning(theTaskID string) bool {
	if taskIDValue, taskIDFound := runningTasks[theTaskID]; taskIDFound {
		taskIDValue = taskIDValue
		return true
	}
	return false
}

// Read the Task's details from its config file.
func getTaskDetails(theTaskID string) (map[string]string, error) {
	taskDetails := make(map[string]string)
	configPath := "tasks/" + theTaskID + "/config.txt"
	// Check to see if we have a valid task ID.
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		inFile, inFileErr := os.Open(configPath)
		if inFileErr != nil {
			return taskDetails, errors.New("Can't open Task config file.")
		} else {
			// Read the Task's details from its config file.
			taskDetails["taskID"] = theTaskID
			taskDetails["title"] = ""
			taskDetails["description"] = ""
			taskDetails["secret"] = ""
			taskDetails["public"] = "N"
			taskDetails["ratelimit"] = "0"
			taskDetails["progress"] = "N"
			taskDetails["command"] = ""
			scanner := bufio.NewScanner(inFile)
			for scanner.Scan() {
				itemSplit := strings.SplitN(scanner.Text(), ":", 2)
				taskDetails[strings.TrimSpace(itemSplit[0])] = strings.TrimSpace(itemSplit[1])
			}
			inFile.Close()
		}
	} else {
		return taskDetails, errors.New("Invalid taskID")
	}
	return taskDetails, nil
}

// Returns a list of task details.
func getTaskList() ([]map[string]string, error) {
	var taskList []map[string]string
	taskIDs, readDirErr := ioutil.ReadDir("tasks")
	if readDirErr == nil {
		for _, taskID := range taskIDs {
			taskDetails, taskErr := getTaskDetails(taskID.Name())
			if taskErr == nil {
				taskList = append(taskList, taskDetails)
			} else {
				return taskList, taskErr
			}
		}
	} else {
		return taskList, errors.New("Can't read Tasks folder.")
	}
	return taskList, nil
}

// Get an input string from the user via stdin.
func getUserInput(defaultValue string, messageString string) string {
	inputReader := bufio.NewReader(os.Stdin)
	fmt.Printf(messageString + ": ")
	result, _ := inputReader.ReadString('\n')
	result = strings.TrimSpace(result)
	if result == "" {
		return defaultValue
	}
	return result
}

// The main body of the program - parse user-provided command-line paramaters, or start the main web server process.
func main() {
	// This application is both a web server for handling API requests and displaying a web-based front end, and a command-line application for handling
	// configuration and setup.
	
	// Set some default argument values.
	arguments["port"] = "8090"
	arguments["localOnly"] = "true"
	arguments["start"] = "true"
	if len(os.Args) != 1 {
		arguments["start"] = "false"
	}
	
	// Parse any command line arguments.
	currentArgKey := ""
	for _, argVal := range os.Args {
		if strings.HasPrefix(argVal, "--") {
			currentArgKey = argVal
		} else {
			if currentArgKey != "" {
				arguments[currentArgKey[2:]] = argVal
			}
			currentArgKey = ""
		}
	}
	// If we have an arument called "config", try and load the given config file (either an Excel or CSV file).
	if configPath, configFound := arguments["config"]; configFound {
		// Is the config file an Excel file?
		if strings.HasSuffix(strings.ToLower(configPath), "xlsx") {
			excelFile, excelErr := excelize.OpenFile(configPath)
			if excelErr == nil {
				excelSheetName := excelFile.GetSheetName(0)
				excelCells, cellErr := excelFile.GetRows(excelSheetName)
				if cellErr == nil {
					fmt.Println(excelCells)
				} else {
					fmt.Println("ERROR: " + cellErr.Error())
				}
			} else {
				fmt.Println("ERROR: " + excelErr.Error())
			}
		} else if strings.HasSuffix(strings.ToLower(configPath), "csv") {
			csvFile, csvErr := os.Open(configPath)
			if csvErr == nil {
				csvData := csv.NewReader(csvFile)
				for {
					csvDataRecord, csvDataErr := csvData.Read()
					if csvDataErr == io.EOF {
						break
					}
					if csvDataErr != nil {
						fmt.Println("ERROR: " + csvDataErr.Error())
					} else {
						arguments[csvDataRecord[0]] = csvDataRecord[1]
					}
				}
			} else {
				fmt.Println("ERROR: " + csvErr.Error())
			}
		}
	}
	
	fmt.Println(arguments)
	
	if (arguments["start"] == "true") {
		// Start the thread that checks for and clears expired tokens.
		go clearExpiredTokens()
		
		// If no parameters are given, simply start the web server.
		fmt.Println("Starting web server...")
		
		// Handle the request URL.
		http.HandleFunc("/", func (theResponseWriter http.ResponseWriter, theRequest *http.Request) {
			// Make sure submitted form values are parsed.
			theRequest.ParseForm()
			
			// The default root - serve index.html.
			if theRequest.URL.Path == "/" {
				http.ServeFile(theResponseWriter, theRequest, "www/index.html")
			// Handle the getPublicTaskList API call (the one API call that doesn't require authentication).
			} else if strings.HasPrefix(theRequest.URL.Path, "/api/getPublicTaskList") {
				taskList, taskErr := getTaskList()
				if taskErr == nil {
					// We return the list of public tasks in JSON format. Note that public tasks might still need a secret to run, "public"
					// here just means that they are listed by this API call for display on the landing page.
					taskListString := "{"
					for _, task := range taskList {
						if task["public"]  == "Y" {
							taskListString = taskListString + "\"" + task["taskID"] + "\":\"" + task["title"] + "\","
						}
					}
					if taskListString == "{" {
						fmt.Fprintf(theResponseWriter, "{}")
					} else {
						fmt.Fprintf(theResponseWriter, taskListString[:len(taskListString)-1] + "}")
					}
				} else {
					fmt.Fprintf(theResponseWriter, "ERROR: " + taskErr.Error())
				}
			// Handle a view, run or API request. taskID needs to be provided as a parameter, either via GET or POST.
			} else if strings.HasPrefix(theRequest.URL.Path, "/view") || strings.HasPrefix(theRequest.URL.Path, "/run") || strings.HasPrefix(theRequest.URL.Path, "/api/") {
				taskID := theRequest.Form.Get("taskID")
				token := theRequest.Form.Get("token")
				if taskID == "" {
					fmt.Fprintf(theResponseWriter, "ERROR: Missing parameter taskID.")
				} else {
					// If we get to this point, we know we have a valid Task ID.
					taskDetails, taskErr := getTaskDetails(taskID)
					if taskErr == nil {
						authorised := false
						authorisationError := "unknown error"
						currentTimestamp := time.Now().Unix()
						rateLimit, rateLimitErr := strconv.Atoi(taskDetails["ratelimit"])
						if rateLimitErr != nil {
							rateLimit = 0
						}
						if token != "" {
							if tokens[token] == 0 {
								authorisationError = "invalid or expired token"
							} else {
								authorised = true
							}
						} else if checkPasswordHash(theRequest.Form.Get("secret"), taskDetails["secret"]) {
							authorised = true
						} else {
							authorisationError = "incorrect secret"
						}
						if authorised {
							// If we get this far, we know the user is authorised for this Task - they've either provided a valid
							// secret or no secret is set.
							if token == "" {
								token = generateRandomString()
							}
							tokens[token] = currentTimestamp
							// Handle view and run requests - no difference server-side, only the client-side treates the URLs differently
							// (the "runTask" method gets called by the client-side code if the URL contains "run" rather than "view").
							if strings.HasPrefix(theRequest.URL.Path, "/view") || strings.HasPrefix(theRequest.URL.Path, "/run") {
								// Serve the webconsole.html file, first adding in the Task ID  and token values to be used client-side.
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
							} else if strings.HasPrefix(theRequest.URL.Path, "/api/getTaskDetails") {
								fmt.Fprintf(theResponseWriter, taskDetails["title"] + "\n" + taskDetails["description"])
							// API - Run a given Task.
							} else if strings.HasPrefix(theRequest.URL.Path, "/api/runTask") {
								// If the Task is already running, simply return "OK".
								if taskIsRunning(taskID) {
									fmt.Fprintf(theResponseWriter, "OK")
								} else {
									// Check to see if there's any rate limit set for this task, and don't run the Task if we're still
									// within the rate limited time.
									if currentTimestamp - taskStopTimes[taskID] < int64(rateLimit) {
										fmt.Fprintf(theResponseWriter, "ERROR: Rate limit (%d seconds) exceeded - try again in %d seconds.", rateLimit, int64(rateLimit) - (currentTimestamp - taskStopTimes[taskID]))
									} else {
										// Get ready to run the Task - set up the Task's details...
										commandArray := parseCommandString(taskDetails["command"])
										var commandArgs []string
										if len(commandArray) > 0 {
											commandArgs = commandArray[1:]
										}
										runningTasks[taskID] = exec.Command(commandArray[0], commandArgs...)
										runningTasks[taskID].Dir = "tasks/" + taskID
										
										// ...get a list (if available) of recent run times...
										taskRunTimes[taskID] = make([]int64, 0)
										runTimesBytes, fileErr := ioutil.ReadFile("tasks/" + taskID + "/runTimes.txt")
										if fileErr == nil {
											runTimeSplit := strings.Split(string(runTimesBytes), "\n")
											for pl := 0; pl < len(runTimeSplit); pl = pl + 1 {
												runTimeVal, runTimeErr := strconv.Atoi(runTimeSplit[pl])
												if runTimeErr == nil {
													taskRunTimes[taskID] = append(taskRunTimes[taskID], int64(runTimeVal))
												}
											}
										}
										
										// ...use those to guess the run time for this time (just use a simple mean of the
										// existing runtimes)...
										var totalRunTime int64
										totalRunTime = 0
										for pl := 0; pl < len(taskRunTimes[taskID]); pl = pl + 1 {
											totalRunTime = totalRunTime + taskRunTimes[taskID][pl]
										}
										if len(taskRunTimes[taskID]) == 0 {
											taskRuntimeGuesses[taskID] = float64(10)
										} else {
											taskRuntimeGuesses[taskID] = float64(totalRunTime / int64(len(taskRunTimes[taskID])))
										}
										taskStartTimes[taskID] = time.Now().Unix()
										
										// ...then run the Task as a goroutine (thread) in the background.
										go runTask(taskID)
										// Respond to the front-end code that all is okay.
										fmt.Fprintf(theResponseWriter, "OK")
									}
								}
							// Designed to be called periodically, will return the given Tasks' output as a simple string,
							// with lines separated by newlines. Takes one parameter, "line", indicating which output line
							// it should return output from, to save the client-side code having to be sent all of the output each time.
							} else if strings.HasPrefix(theRequest.URL.Path, "/api/getTaskOutput") {
								var atoiErr error
								// Parse the "line" parameter - defaults to 0, so if not set this method will simply return
								// all current output.
								outputLineNumber := 0
								if theRequest.Form.Get("line") != "" {
									outputLineNumber, atoiErr = strconv.Atoi(theRequest.Form.Get("line"))
									if atoiErr != nil {
										fmt.Fprintf(theResponseWriter, "ERROR: Line number not parsable.")
									}
								}
								// If the job details have the "progress" option set to "Y", output a (best guess, using previous
								// run times) progresss report line.
								if taskDetails["progress"] == "Y" {
									currentTime := time.Now().Unix()
									percentage := int((float64(currentTime - taskStartTimes[taskID]) / taskRuntimeGuesses[taskID]) * 100)
									if percentage > 100 {
										percentage = 100
									}
									taskOutputs[taskID] = append(taskOutputs[taskID], fmt.Sprintf("Progress: Progress %d%%", percentage))
								}
								// Return to the user all the output lines from the given starting point.
								for outputLineNumber < len(taskOutputs[taskID]) {
									fmt.Fprintln(theResponseWriter, taskOutputs[taskID][outputLineNumber])
									outputLineNumber = outputLineNumber + 1
								}
								// If the Task is no longer running, make sure we tell the client-side code that.
								if _, runningTaskFound := runningTasks[taskID]; !runningTaskFound {
									if taskDetails["progress"] == "Y" {
										fmt.Fprintf(theResponseWriter, "Progress: Progress 100%%\n")
									}
									fmt.Fprintf(theResponseWriter, "ERROR: EOF")
									//delete(taskOutputs, taskID)
								}
							// Simply returns "YES" if a given Task is running, "NO" otherwise.
							} else if strings.HasPrefix(theRequest.URL.Path, "/api/getTaskRunning") {
								if taskIsRunning(taskID) {
									fmt.Fprintf(theResponseWriter, "YES")
								} else {
									fmt.Fprintf(theResponseWriter, "NO")
								}
							// A simple call that doesn't do anything except serve to keep the timestamp for the given Task up-to-date.
							} else if strings.HasPrefix(theRequest.URL.Path, "/api/keepAlive") {
								fmt.Fprintf(theResponseWriter, "OK")
							// To do: return API documentation here.
							} else if strings.HasPrefix(theRequest.URL.Path, "/api/") {
								fmt.Fprintf(theResponseWriter, "ERROR: Unknown API call: %s", theRequest.URL.Path)
							}
						} else {
							fmt.Fprintf(theResponseWriter, "ERROR: Not authorised - %s.", authorisationError)
						}
					} else {
						fmt.Fprintf(theResponseWriter, "ERROR: %s", taskErr.Error())
					}
				}
			// Otherwise, try and find the static file referred to by the request URL.
			} else {
				http.ServeFile(theResponseWriter, theRequest, "www" + theRequest.URL.Path)
			}
		})
		// Run the main web server loop.
		// To do: replace with Caddy so we can handle HTTPS easily.
		log.Fatal(http.ListenAndServe(":8090", nil))
	// Command-line option to print a list of all Tasks.
	} else if os.Args[1] == "-list" {
		taskList, taskErr := getTaskList()
		if taskErr == nil {
			for _, task := range taskList {
				secret := "Y"
				if task["secret"] == "" {
					secret = "N"
				}
				fmt.Println(task["taskID"] + ": " + task["title"] + ", Secret: " + secret + ", Public: " + task["public"] + ", Command: " + task["command"])
			}
		} else {
			fmt.Println("ERROR: " + taskErr.Error())
		}
	// Command-line option to generate a new Task.
	} else if os.Args[1] == "-new" {
		// Generate a new, unique Task ID.
		var newTaskID string
		for {
			newTaskID = generateRandomString()
			if _, err := os.Stat("tasks/" + newTaskID); os.IsNotExist(err) {
				break
			}
		}
		// Ask the user to provide a Task ID (or they can use the one we just generated).
		newTaskID = getUserInput(newTaskID, "Enter a new Task ID (hit enter to generate an ID)")
		if _, err := os.Stat("tasks/" + newTaskID); os.IsNotExist(err) {
			// We use simple text files in folders for data storage, rather than a database. It seemed the most logical choice - you can stick
			// any resources associated with a Task in that Task's folder, and editing options can be done with a basic text editor.
			os.Mkdir("tasks/" + newTaskID, os.ModePerm)
			fmt.Println("New Task: " + newTaskID)
			
			// Get a title for the Task.
			newTaskTitle := "Task " + newTaskID
			newTaskTitle = getUserInput(newTaskTitle, "Enter a title (hit enter for \"" + newTaskTitle + "\")")
			
			// Get a secret for the Task - blank by default, although that's not the same as a public Task.
			newTaskSecret := ""
			newTaskSecret = getUserInput(newTaskSecret, "Set secret (type secret, or hit enter to skip)")
			
			// Ask the user if this Task should be public, "N" by default.
			var newTaskPublic string
			for {
				newTaskPublic = "N"
				newTaskPublic = strings.ToUpper(getUserInput(newTaskPublic, "Make this task public (\"Y\" or \"N\", hit enter for \"N\")"))
				if newTaskPublic == "Y" || newTaskPublic == "N" {
					break
				}
			}
			
			// The command the Task runs. Can be anything the system will run as an executable application, which of course depends on which platform
			// you are running.
			newTaskCommand := ""
			newTaskCommand = getUserInput(newTaskCommand, "Set command (type command, or hit enter to skip)")
			
			// Hash the secret (if not just blank).
			outputString := ""
			if newTaskSecret != "" {
				hashedPassword, hashErr := hashPassword(newTaskSecret)
				if hashErr == nil {
					outputString = outputString + "secret: " + hashedPassword + "\n"
				} else {
					fmt.Println("ERROR: Problem hashing password - " + hashErr.Error())
				}
			}
			
			// Write the config file - a simple text file, one value per line.
			outputString = outputString + "title: " + newTaskTitle + "\npublic: " + newTaskPublic + "\ncommand: " + newTaskCommand
			writeFileErr := ioutil.WriteFile("tasks/" + newTaskID + "/config.txt", []byte(outputString), 0644)
			if writeFileErr != nil {
				fmt.Println("ERROR: Couldn't write config for Task " + newTaskID + ".")
			}
		} else {
			fmt.Println("ERROR: A task with ID " + newTaskID + " already exists.")
		}		
	}
}
