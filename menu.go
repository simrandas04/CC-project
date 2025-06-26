package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const baseURL = "http://localhost:8080"

func main() {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("\n====== Raft3D Terminal Menu ======")
		fmt.Println("1. Add Printer")
		fmt.Println("2. List Printers")
		fmt.Println("3. Add Filament")
		fmt.Println("4. List Filaments")
		fmt.Println("5. Add Print Job")
		fmt.Println("6. List Print Jobs")
		fmt.Println("7. Update Print Job Status")
		fmt.Println("8. Exit")
		fmt.Print("Select option: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			addPrinter(reader)
		case "2":
			get("/printers")
		case "3":
			addFilament(reader)
		case "4":
			get("/filaments")
		case "5":
			addPrintJob(reader)
		case "6":
			get("/printjobs")
		case "7":
			updatePrintJobStatus(reader)
		case "8":
			fmt.Println("Exiting Raft3D CLI. Goodbye!")
			return
		default:
			fmt.Println("Invalid choice. Try again.")
		}
	}
}

// === Helper Functions ===

func addPrinter(reader *bufio.Reader) {
	fmt.Print("Enter company: ")
	company, _ := reader.ReadString('\n')
	fmt.Print("Enter model: ")
	model, _ := reader.ReadString('\n')

	data := map[string]string{
		"company": strings.TrimSpace(company),
		"model":   strings.TrimSpace(model),
	}
	post("/printers", data)
}

func addFilament(reader *bufio.Reader) {
	fmt.Print("Enter type (PLA, ABS, PETG, TPU): ")
	ftype, _ := reader.ReadString('\n')
	fmt.Print("Enter colour: ")
	colour, _ := reader.ReadString('\n')
	fmt.Print("Enter total weight in grams: ")
	var weight int
	fmt.Scanln(&weight)

	data := map[string]interface{}{
		"type":                    strings.TrimSpace(ftype),
		"colour":                  strings.TrimSpace(colour),
		"total_weight_in_grams":   weight,
		"remaining_weight_in_grams": weight,
	}
	post("/filaments", data)
}

func addPrintJob(reader *bufio.Reader) {
	fmt.Print("Enter printer ID: ")
	pid, _ := reader.ReadString('\n')
	fmt.Print("Enter filament ID: ")
	fid, _ := reader.ReadString('\n')
	fmt.Print("Enter .gcode filepath: ")
	fp, _ := reader.ReadString('\n')
	fmt.Print("Enter print weight in grams: ")
	var weight int
	fmt.Scanln(&weight)

	data := map[string]interface{}{
		"printer_id":            strings.TrimSpace(pid),
		"filament_id":           strings.TrimSpace(fid),
		"filepath":              strings.TrimSpace(fp),
		"print_weight_in_grams": weight,
	}
	post("/printjobs", data)
}

func updatePrintJobStatus(reader *bufio.Reader) {
	fmt.Print("Enter print job ID: ")
	jid, _ := reader.ReadString('\n')
	fmt.Print("Enter new status (running, done, canceled): ")
	status, _ := reader.ReadString('\n')

	endpoint := fmt.Sprintf("/printjobs/update?id=%s&status=%s",
		strings.TrimSpace(jid),
		strings.TrimSpace(status),
	)

	resp, err := http.Post(baseURL+endpoint, "application/json", nil)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	printJSON(out)
}

func post(path string, payload interface{}) {
	body, _ := json.Marshal(payload)
	resp, err := http.Post(baseURL+path, "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	printJSON(out)
}

func get(path string) {
	resp, err := http.Get(baseURL + path)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()

	var out interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	printJSON(out)
}

func printJSON(data interface{}) {
	b, _ := json.MarshalIndent(data, "", "  ")
	fmt.Println(string(b))
}
