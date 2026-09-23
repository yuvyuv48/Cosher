package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const dataDir = "data"

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

func weekdays() []time.Weekday {
	return []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
		time.Friday, time.Saturday}
}

// ---------- storage helpers ----------

func readCSV(name string) ([][]string, error) {
	f, err := os.Open(filepath.Join(dataDir, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	return r.ReadAll()
}

func writeCSV(name string, rows [][]string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dataDir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	return csv.NewWriter(f).WriteAll(rows)
}

func appendCSV(name string, row []string) error {
	rows, err := readCSV(name)
	if err != nil {
		return err
	}
	rows = append(rows, row)
	return writeCSV(name, rows)
}

// ---------- domain ----------

type PlannedExercise struct {
	Weekday time.Weekday
	Name    string
	Sets    int
	Reps    int
	Weight  float64
}

type Plan struct {
	User       string
	StartDate  time.Time
	BlockWeeks int
}

// ---------- commands ----------

func cmdUserCreate(name string) error {
	users, _ := readCSV("users.csv")
	for _, u := range users {
		if u[0] == name {
			return fmt.Errorf("user %q already exists", name)
		}
	}
	return appendCSV("users.csv", []string{name})
}

func cmdExerciseCreate(name string) error {
	exs, _ := readCSV("exercises.csv")
	for _, e := range exs {
		if e[0] == name {
			return fmt.Errorf("exercise %q already exists", name)
		}
	}
	return appendCSV("exercises.csv", []string{name})
}

func cmdPlanCreate(username string) error {
	if _, err := getUserPlan(username); err == nil {
		return fmt.Errorf("user %q already has a plan (delete data/plans.csv to reset)", username)
	}

	reader := bufio.NewReader(os.Stdin)
	var planned []PlannedExercise

	fmt.Println("Plan setup — for each weekday, enter an exercise name (must exist in data/exercises.csv), or press Enter for a rest day.")

	for _, wd := range weekdays() {
		fmt.Printf("\n%s\n", wd)
		exName := ask(reader, "  exercise (enter = rest day): ")
		if strings.TrimSpace(exName) == "" {
			continue
		}
		if !exerciseExists(exName) {
			fmt.Printf("  exercise %q not found. Create it? (y/n): ", exName)
			ans := ask(reader, "")
			if strings.EqualFold(strings.TrimSpace(ans), "y") {
				if err := cmdExerciseCreate(exName); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("exercise %q does not exist", exName)
			}
		}
		sets := askInt(reader, "  sets [3]: ", 3)
		reps := askInt(reader, "  reps [10]: ", 10)
		weight := askFloat(reader, "  weight kg [0 = bodyweight/unweighted]: ", 0)
		planned = append(planned, PlannedExercise{Weekday: wd, Name: exName, Sets: sets, Reps: reps, Weight: weight})
	}

	blockWeeks := askInt(reader, "\nblock size in weeks [4]: ", 4)

	// persist plan template
	var rows [][]string
	for _, p := range planned {
		rows = append(rows, []string{username, p.Weekday.String(), p.Name,
			strconv.Itoa(p.Sets), strconv.Itoa(p.Reps),
			strconv.FormatFloat(p.Weight, 'f', -1, 64)})
	}
	if err := writeCSV("planned.csv", rows); err != nil {
		return err
	}
	fmt.Printf("\nPlan template saved for %q (%d training days). Use `plan start` to set the start date.\n", username, len(planned))
	return nil
}

func cmdPlanAddExercise(username, weekday, exName string, sets, reps int, weight float64) error {
	wd, err := parseWeekday(weekday)
	if err != nil {
		return err
	}
	if !exerciseExists(exName) {
		return fmt.Errorf("exercise %q does not exist (add it with `exercise create`)", exName)
	}
	return appendCSV("planned.csv", []string{username, wd.String(), exName,
		strconv.Itoa(sets), strconv.Itoa(reps),
		strconv.FormatFloat(weight, 'f', -1, 64)})
}

func cmdPlanStart(username, dateStr string) error {
	start, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return fmt.Errorf("date must be YYYY-MM-DD")
	}
	if _, err := getUserPlan(username); err == nil {
		return fmt.Errorf("user %q already has a started plan", username)
	}
	blockWeeks := 4
	rows, _ := readCSV("plans.csv")
	for _, r := range rows { // keep block size if template had one
		if r[0] == username {
			blockWeeks, _ = strconv.Atoi(r[2])
		}
	}
	return appendCSV("plans.csv", []string{username, start.Format("2006-01-02"), strconv.Itoa(blockWeeks)})
}

func cmdSessionGet(username string) error {
	plan, err := getUserPlan(username)
	if err != nil {
		return err
	}
	planned, err := getUserPlanned(username)
	if err != nil {
		return err
	}

	today := time.Now().Truncate(24 * time.Hour)
	daysSince := int(today.Sub(plan.StartDate).Hours() / 24)
	if daysSince < 0 {
		return fmt.Errorf("plan starts on %s (in %d days)", plan.StartDate.Format("2006-01-02"), -daysSince)
	}

	week := daysSince/7 + 1
	if week > plan.BlockWeeks {
		return fmt.Errorf("plan finished (block was %d weeks, now in week %d). Create a new plan.", plan.BlockWeeks, week)
	}

	todayPlanned := filterByWeekday(planned, today.Weekday())

	fmt.Printf("User:     %s\nWeek:     %d of %d\nDay:      %s (%s)\n",
		username, week, plan.BlockWeeks, today.Format("2006-01-02"), today.Weekday())

	if len(todayPlanned) == 0 {
		fmt.Println("Today is a rest day.")
		return nil
	}

	fmt.Println("Today's session:")
	alreadyLogged, err := isLogged(username, today)
	if err != nil {
		return err
	}

	for _, p := range todayPlanned {
		done := ""
		if alreadyLogged[p.Name] {
			done = "  [logged]"
		}
		fmt.Printf("  %s: %dx%d @ %.1f kg%s\n", p.Name, p.Sets, p.Reps, p.Weight, done)
	}

	if !allLogged(alreadyLogged, todayPlanned) && confirm("Log this session now? (y/n): ") {
		var rows [][]string
		for _, p := range todayPlanned {
			if !alreadyLogged[p.Name] {
				rows = append(rows, []string{username, today.Format("2006-01-02"), p.Name,
					strconv.Itoa(p.Sets), strconv.Itoa(p.Reps),
					strconv.FormatFloat(p.Weight, 'f', -1, 64), "yes"})
			}
		}
		if err := appendCSV("logged.csv", rows...); err != nil {
			return err
		}
		fmt.Println("Session logged.")
	}
	return nil
}

// ---------- helpers ----------

func getUserPlan(user string) (Plan, error) {
	rows, err := readCSV("plans.csv")
	if err != nil {
		return Plan{}, err
	}
	for _, r := range rows {
		if r[0] == user {
			start, err := time.Parse("2006-01-02", r[1])
			if err != nil {
				return Plan{}, fmt.Errorf("bad start date in plans.csv for %q", user)
			}
			w, _ := strconv.Atoi(r[2])
			return Plan{User: user, StartDate: start, BlockWeeks: w}, nil
		}
	}
	return Plan{}, fmt.Errorf("no plan for user %q (create one with `plan create` and start it with `plan start`)", user)
}

func getUserPlanned(user string) ([]PlannedExercise, error) {
	rows, err := readCSV("planned.csv")
	if err != nil {
		return nil, err
	}
	var out []PlannedExercise
	for _, r := range rows {
		if r[0] != user {
			continue
		}
		wd, err := parseWeekday(r[1])
		if err != nil {
			return nil, err
		}
		sets, _ := strconv.Atoi(r[3])
		reps, _ := strconv.Atoi(r[4])
		weight, _ := strconv.ParseFloat(r[5], 64)
		out = append(out, PlannedExercise{Weekday: wd, Name: r[2], Sets: sets, Reps: reps, Weight: weight})
	}
	return out, nil
}

func filterByWeekday(ps []PlannedExercise, wd time.Weekday) []PlannedExercise {
	var out []PlannedExercise
	for _, p := range ps {
		if p.Weekday == wd {
			out = append(out, p)
		}
	}
	return out
}

func isLogged(user string, date time.Time) (map[string]bool, error) {
	rows, err := readCSV("logged.csv")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	ds := date.Format("2006-01-02")
	for _, r := range rows {
		if r[0] == user && r[1] == ds {
			out[r[2]] = true
		}
	}
	return out, nil
}

func allLogged(logged map[string]bool, planned []PlannedExercise) bool {
	for _, p := range planned {
		if !logged[p.Name] {
			return false
		}
	}
	return true
}

func exerciseExists(name string) bool {
	rows, _ := readCSV("exercises.csv")
	for _, r := range rows {
		if r[0] == name {
			return true
		}
	}
	return false
}

func parseWeekday(s string) (time.Weekday, error) {
	s = strings.TrimSpace(s)
	for i, name := range weekdayNames {
		if strings.EqualFold(name, s) || strings.EqualFold(name[:3], s) {
			return weekdays()[i], nil
		}
	}

	return 0, fmt.Errorf("unknown weekday %q (use monday..sunday)", s)
}

func ask(r *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

func askInt(r *bufio.Reader, prompt string, def int) int {
	for {
		s := ask(r, prompt)
		if s == "" {
			return def
		}
		n, err := strconv.Atoi(s)
		if err == nil && n > 0 {
			return n
		}
		fmt.Println("  please enter a positive number")
	}
}

func askFloat(r *bufio.Reader, prompt string, def float64) float64 {
	for {
		s := ask(r, prompt)
		if s == "" {
			return def
		}
		f, err := strconv.ParseFloat(s, 64)
		if err == nil && f >= 0 {
			return f
		}
		fmt.Println("  please enter a number (e.g. 87.5)")
	}
}

func confirm(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)
	return strings.EqualFold(strings.TrimSpace(ask(reader, prompt)), "y")
}

// ---------- main ----------

func usage() {
	fmt.Println(`usage:
  user create <name>
  exercise create <name>
  plan create <user>            # interactive: pick exercises per weekday
  plan start <user> <YYYY-MM-DD>
  plan add-exercise <user> <weekday> <exercise> <sets> <reps> <weight_kg>
  session get <user>`)
}

func main() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(1)
	}

	var err error
	switch {
	case os.Args[1] == "user" && os.Args[2] == "create" && len(os.Args) == 4:
		err = cmdUserCreate(os.Args[3])
	case os.Args[1] == "exercise" && os.Args[2] == "create" && len(os.Args) == 4:
		err = cmdExerciseCreate(os.Args[3])
	case os.Args[1] == "plan" && os.Args[2] == "create" && len(os.Args) == 4:
		err = cmdPlanCreate(os.Args[3])
	case os.Args[1] == "plan" && os.Args[2] == "start" && len(os.Args) == 5:
		err = cmdPlanStart(os.Args[3], os.Args[4])
	case os.Args[1] == "plan" && os.Args[2] == "add-exercise" && len(os.Args) == 9:
		sets, e1 := strconv.Atoi(os.Args[6])
		reps, e2 := strconv.Atoi(os.Args[7])
		weight, e3 := strconv.ParseFloat(os.Args[8], 64)
		if e1 != nil || e2 != nil || e3 != nil {
			err = fmt.Errorf("sets/reps must be ints, weight a number")
		} else {
			err = cmdPlanAddExercise(os.Args[3], os.Args[4], os.Args[5], sets, reps, weight)
		}
	case os.Args[1] == "session" && os.Args[2] == "get" && len(os.Args) == 4:
		err = cmdSessionGet(os.Args[3])
	default:
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
