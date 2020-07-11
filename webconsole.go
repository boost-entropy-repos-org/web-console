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
	"math/rand"
	"io/ioutil"
	"net/http"
)

// Characters to use to generate new ID strings. Lowercase only - any user-provided IDs will be lowercased before use.
const letters = "abcdefghijklmnopqrstuvwxyz1234567890"

// Generate a new, random 16-character ID.
func generateTaskID() string {
	rand.Seed(time.Now().UnixNano())
	result := make([]byte, 16)
	for pl := range result {
		result[pl] = letters[rand.Intn(len(letters))]
	}
	return string(result)
}

// The main body of the program - parse user-provided command-line paramaters, or start the main web server process.
func main() {
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
			// Handle a View Task request. taskID needs to be provided as a parameter, either via GET or POST.
			} else if strings.HasPrefix(theRequest.URL.Path, "/view") {
				taskID := theRequest.Form.Get("taskID")
				if taskID != "" {
					// Check to see if we have a valid task ID.
					if _, err := os.Stat("tasks/" + taskID); !os.IsNotExist(err) {
						// Serve the webconsole.html file, first adding in the Task ID value so it can be used client-side.
						webconsoleBuffer, fileErr := ioutil.ReadFile("www/webconsole.html")
						if fileErr == nil {
							webconsoleString := string(webconsoleBuffer)
							webconsoleString = strings.Replace(webconsoleString, "taskID = \"\"", "taskID = \"" + taskID + "\"", -1)
							http.ServeContent(theResponseWriter, theRequest, "webconsole.html", time.Now(), strings.NewReader(webconsoleString))
						}
					}
				}
			// Handle API calls.
			} else if strings.HasPrefix(theRequest.URL.Path, "/api/getTaskDetails") {
				taskID := theRequest.Form.Get("taskID")
				if taskID != "" {
					// See if we have a config file.
					configPath := "tasks/" + taskID + "/config.txt"
					if _, err := os.Stat(configPath); !os.IsNotExist(err) {
						inFile, inFileErr := os.Open(configPath)
						if inFileErr != nil {
							log.Fatal(inFileErr)
						}
						taskTitle := ""
						scanner := bufio.NewScanner(inFile)
						for scanner.Scan() {
							if strings.HasPrefix(scanner.Text(), "title:") {
								taskTitle = strings.TrimSpace(scanner.Text()[5:])
							}
						}
						inFile.Close()
						fmt.Fprintf(theResponseWriter, "[\"title\":\"%s\"]", taskTitle)
					}
				}
			} else if strings.HasPrefix(theRequest.URL.Path, "/api/") {
				fmt.Fprintf(theResponseWriter, "API call: %s", theRequest.URL.Path)
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
			newTaskID := generateTaskID()
			if _, err := os.Stat("tasks/" + newTaskID); os.IsNotExist(err) {
				os.Mkdir("tasks/" + newTaskID, os.ModePerm)
				fmt.Println("New Task generated: " + newTaskID)
				break
			}
		}
	}
}
