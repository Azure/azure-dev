package models

import (
	"fmt"
	"time"
)

// FineTuningJobTableView is the table display representation for job listings
type FineTuningJobTableView struct {
	ID        string    `table:"ID"`
	Status    JobStatus `table:"Status"`
	BaseModel string    `table:"Model"`
	CreatedAt time.Time `table:"Created"`
}

// JobDetailsView is the basic job info section
type JobDetailsView struct {
	ID             string    `table:"ID"`
	Status         JobStatus `table:"Status"`
	Model          string    `table:"Model"`
	FineTunedModel string    `table:"Fine-tuned Model"`
}

// TimestampsView is the timestamps section
type TimestampsView struct {
	Created      string `table:"Created"`
	Finished     string `table:"Finished"`
	EstimatedETA string `table:"Estimated ETA"`
}

// BaseConfigurationView has fields common to all methods
type BaseConfigurationView struct {
	TrainingType string `table:"Training Type"`
	Epochs       int64  `table:"Epochs"`
	BatchSize    int64  `table:"Batch Size"`
	LearningRate string `table:"Learning Rate"`
}

// DPOConfigurationView has DPO-specific fields
type DPOConfigurationView struct {
	TrainingType string `table:"Training Type"`
	Epochs       int64  `table:"Epochs"`
	BatchSize    int64  `table:"Batch Size"`
	LearningRate string `table:"Learning Rate"`
	Beta         string `table:"Beta"`
}

// ReinforcementConfigurationView has reinforcement-specific fields
type ReinforcementConfigurationView struct {
	TrainingType      string `table:"Training Type"`
	Epochs            int64  `table:"Epochs"`
	BatchSize         int64  `table:"Batch Size"`
	LearningRate      string `table:"Learning Rate"`
	ComputeMultiplier string `table:"Compute Multiplier"`
	EvalInterval      string `table:"Eval Interval"`
	EvalSamples       string `table:"Eval Samples"`
	ReasoningEffort   string `table:"Reasoning Effort"`
}

// DataView is the training/validation data section
type DataView struct {
	TrainingFile   string `table:"Training File"`
	ValidationFile string `table:"Validation File"`
}

// JobDetailViews contains all view sections for a job detail display
type JobDetailViews struct {
	Details       *JobDetailsView
	Timestamps    *TimestampsView
	Configuration interface{} // Can be Base, DPO, or Reinforcement view
	Data          *DataView
}

// ToTableView converts a FineTuningJob to its table view (for list command)
func (j *FineTuningJob) ToTableView() *FineTuningJobTableView {
	return &FineTuningJobTableView{
		ID:        j.ID,
		Status:    j.Status,
		BaseModel: j.BaseModel,
		CreatedAt: j.CreatedAt,
	}
}

// ToDetailViews converts a FineTuningJobDetail to its sectioned views (for show command)
func (j *FineTuningJobDetail) ToDetailViews() *JobDetailViews {
	fineTunedModel := j.FineTunedModel
	if fineTunedModel == "" {
		fineTunedModel = "-"
	}

	// Build configuration view based on method type
	var configView interface{}
	switch j.Method {
	case string(DPO):
		configView = &DPOConfigurationView{
			TrainingType: j.Method,
			Epochs:       j.Hyperparameters.NEpochs,
			BatchSize:    j.Hyperparameters.BatchSize,
			LearningRate: formatFloatOrDash(j.Hyperparameters.LearningRateMultiplier),
			Beta:         formatFloatOrDash(j.Hyperparameters.Beta),
		}
	case string(Reinforcement):
		configView = &ReinforcementConfigurationView{
			TrainingType:      j.Method,
			Epochs:            j.Hyperparameters.NEpochs,
			BatchSize:         j.Hyperparameters.BatchSize,
			LearningRate:      formatFloatOrDash(j.Hyperparameters.LearningRateMultiplier),
			ComputeMultiplier: formatFloatOrDash(j.Hyperparameters.ComputeMultiplier),
			EvalInterval:      formatInt64OrDash(j.Hyperparameters.EvalInterval),
			EvalSamples:       formatInt64OrDash(j.Hyperparameters.EvalSamples),
			ReasoningEffort:   stringOrDash(j.Hyperparameters.ReasoningEffort),
		}
	default: // supervised or unknown
		configView = &BaseConfigurationView{
			TrainingType: j.Method,
			Epochs:       j.Hyperparameters.NEpochs,
			BatchSize:    j.Hyperparameters.BatchSize,
			LearningRate: formatFloatOrDash(j.Hyperparameters.LearningRateMultiplier),
		}
	}

	return &JobDetailViews{
		Details: &JobDetailsView{
			ID:             j.ID,
			Status:         j.Status,
			Model:          j.Model,
			FineTunedModel: fineTunedModel,
		},
		Timestamps: &TimestampsView{
			Created:      formatTimeOrDash(j.CreatedAt),
			Finished:     formatTimePointerOrDash(j.FinishedAt),
			EstimatedETA: formatTimePointerOrDash(j.EstimatedFinish),
		},
		Configuration: configView,
		Data: &DataView{
			TrainingFile:   j.TrainingFile,
			ValidationFile: stringOrDash(j.ValidationFile),
		},
	}
}

// ToTableViews converts a slice of jobs to table views
func ToTableViews(job *FineTuningJob) *FineTuningJobTableView {
	view := job.ToTableView()
	return view
}

func formatFloat(f float64) string {
	return fmt.Sprintf("%g", f)
}

func formatFloatOrDash(f float64) string {
	if f == 0 {
		return "-"
	}
	return fmt.Sprintf("%g", f)
}

func formatInt64OrDash(i int64) string {
	if i == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", i)
}

func stringOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Add this helper
func formatTimeOrDash(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04")
}

func formatTimePointerOrDash(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04")
}
