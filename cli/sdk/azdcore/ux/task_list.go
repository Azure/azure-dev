package ux

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"dario.cat/mergo"
	"github.com/fatih/color"
)

type TaskListConfig struct {
	// The writer to use for output (default: os.Stdout)
	Writer       io.Writer
	SuccessStyle string
	ErrorStyle   string
	WarningStyle string
	RunningStyle string
	SkippedStyle string
}

var DefaultTaskListConfig TaskListConfig = TaskListConfig{
	Writer:       os.Stdout,
	SuccessStyle: color.GreenString("(✔) Done "),
	ErrorStyle:   color.RedString("(x) Error "),
	WarningStyle: color.YellowString("(!) Warning "),
	RunningStyle: color.CyanString("(-) Running "),
	SkippedStyle: color.HiBlackString("(o) Skipped "),
}

type TaskList struct {
	canvas Canvas

	config    *TaskListConfig
	tasks     []*Task
	completed int32
}

type Task struct {
	Title     string
	Action    func() (TaskState, error)
	State     TaskState
	Error     error
	startTime *time.Time
	endTime   *time.Time
}

type TaskState int

const (
	Running TaskState = iota
	Skipped
	Warning
	Error
	Success
)

func NewTaskList(config *TaskListConfig) *TaskList {
	mergedConfig := TaskListConfig{}
	if err := mergo.Merge(&mergedConfig, config, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	if err := mergo.Merge(&mergedConfig, DefaultTaskListConfig, mergo.WithoutDereference); err != nil {
		panic(err)
	}

	return &TaskList{
		config: &mergedConfig,
	}
}

func (t *TaskList) WithCanvas(canvas Canvas) Visual {
	t.canvas = canvas
	return t
}

func (t *TaskList) Run() error {
	if t.canvas == nil {
		t.canvas = NewCanvas(t)
	}

	return t.canvas.Run()
}

func (t *TaskList) AddTask(title string, action func() (TaskState, error)) *TaskList {
	if t.canvas == nil {
		t.canvas = NewCanvas(t)
	}

	task := &Task{
		Title:  title,
		Action: action,
		State:  Running,
	}

	go func() {
		task.startTime = Ptr(time.Now())
		state, err := task.Action()
		task.endTime = Ptr(time.Now())

		if err != nil {
			task.Error = err
		}

		task.State = state

		atomic.AddInt32(&t.completed, 1)
		t.canvas.Update()
	}()

	t.tasks = append(t.tasks, task)
	t.canvas.Update()

	return t
}

func (t *TaskList) Completed() bool {
	return int(t.completed) == len(t.tasks)
}

func (t *TaskList) Update() error {
	return t.canvas.Update()
}

func (t *TaskList) Render(printer Printer) error {
	for _, task := range t.tasks {
		endTime := time.Now()
		if task.endTime != nil {
			endTime = *task.endTime
		}

		var elapsedText string
		if task.startTime != nil {
			elapsed := endTime.Sub(*task.startTime)
			elapsedText = color.HiBlackString("(%s)", durationAsText(elapsed))
		}

		switch task.State {
		case Running:
			printer.Fprintf("%s %s %s\n", t.config.RunningStyle, task.Title, elapsedText)
		case Warning:
			printer.Fprintf("%s %s  %s\n", t.config.WarningStyle, task.Title, elapsedText)
		case Error:
			printer.Fprintf(
				"%s %s %s %s\n",
				t.config.ErrorStyle,
				task.Title,
				elapsedText,
				color.RedString("(%s)", task.Error.Error()),
			)
		case Success:
			printer.Fprintf("%s %s  %s\n", t.config.SuccessStyle, task.Title, elapsedText)
		case Skipped:
			printer.Fprintf("%s %s %s\n", t.config.SkippedStyle, task.Title, color.RedString("(%s)", task.Error.Error()))
		}
	}

	return nil
}

// DurationAsText provides a slightly nicer string representation of a duration
// when compared to default formatting in go, by spelling out the words hour,
// minute and second and providing some spacing and eliding the fractional component
// of the seconds part.
func durationAsText(d time.Duration) string {
	if d.Seconds() < 1.0 {
		return "less than a second"
	}

	var builder strings.Builder

	if (d / time.Hour) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Hour), "hour")
		d = d - ((d / time.Hour) * time.Hour)
	}

	if (d / time.Minute) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Minute), "minute")
		d = d - ((d / time.Minute) * time.Minute)
	}

	if (d / time.Second) > 0 {
		writePart(&builder, fmt.Sprintf("%d", d/time.Second), "second")
	}

	return builder.String()
}

// writePart writes the string [part] followed by [unit] into [builder], unless
// part is empty or the string "0". If part is "1", the [unit] string is suffixed
// with s. If builder is non empty, the written string is preceded by a space.
func writePart(builder *strings.Builder, part string, unit string) {
	if part != "" && part != "0" {
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}

		builder.WriteString(part)
		builder.WriteByte(' ')
		builder.WriteString(unit)
		if part != "1" {
			builder.WriteByte('s')
		}
	}
}
