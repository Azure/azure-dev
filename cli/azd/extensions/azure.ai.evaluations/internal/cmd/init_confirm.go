// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"path/filepath"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// initContext is what the prompt sequence reads and does not change.
type initContext struct {
	cfg        *project.EvalConfig
	azdProject *azdext.ProjectConfig
	evalDir    string
	configPath string
	// configExisted distinguishes a file init is adding to from one it is
	// about to create, which is what the reader is being told.
	configExisted bool
	// tracesWired is memoized by the caller, so asking it again inside a
	// second pass costs nothing.
	tracesWired func() bool
}

// initAnswers is everything the prompt sequence settles.
type initAnswers struct {
	source          string
	target          string
	judgeModel      string
	evalName        string
	datasetRef      string
	evaluationLevel string
	evaluators      []string
	// evaluatorsChosen distinguishes a list someone picked from one that was
	// defaulted, so the summary does not read a selection back to whoever
	// just made it.
	evaluatorsChosen bool
	lookbackHours    int
}

// ask runs the prompt sequence in the order the answers depend on each other.
//
// It is a method rather than a straight line inside Run because the
// confirmation can send the reader back through it, and a second pass has to
// ask the same questions from the same starting point -- not from whatever the
// first pass left behind.
func (a *initAction) ask(ctx initContext) (initAnswers, error) {
	answers := initAnswers{}

	source, err := settleInitSource(a.cmd, initSourceInput{
		explicit:       a.flags.source,
		maxTracesGiven: a.cmd.Flags().Changed("max-traces"),
		traceDaysGiven: a.cmd.Flags().Changed("trace-days"),
		usableDatasets: usableDatasetCount(ctx.cfg, ctx.evalDir),
		tracesWired:    ctx.tracesWired,
	})
	if err != nil {
		return initAnswers{}, err
	}
	answers.source = source

	// The target is what the whole scaffold is named and shaped around, so it
	// is settled before anything derived from it.
	answers.target = a.flags.target
	if answers.target == "" {
		if answers.target, err = resolveAgentTarget(a.cmd, ctx.azdProject); err != nil {
			return initAnswers{}, err
		}
	}
	answers.judgeModel = a.flags.judgeModel
	if answers.judgeModel == "" {
		if answers.judgeModel, err = resolveJudgeModel(a.cmd, ctx.azdProject); err != nil {
			return initAnswers{}, err
		}
	}

	// Reported here, after the values it names are settled and before the
	// questions derived from them. A reader answering "which dataset" needs to
	// know which project and which file they are answering about.
	if !noPrompt(a.cmd) && !isJSON(a.cmd) {
		writeLocalContext(a.cmd.OutOrStdout(), ctx, answers.target, answers.judgeModel)
	}

	// A name someone typed is theirs, so a collision is refused rather than
	// worked around. A name init suggested is init's problem: suggesting one
	// already taken and then refusing it is the command failing on its own
	// proposal.
	answers.evalName = a.flags.evalName
	if answers.evalName == "" {
		answers.evalName = uniqueEvalName(ctx.cfg, defaultEvalName(answers.target, source))
	} else if ctx.cfg.HasEval(answers.evalName) && !a.flags.force {
		// Refused before the remaining prompts as well as after them, so a
		// name that is already taken is reported without asking first.
		return initAnswers{}, messages.EvalAlreadyDeclared(
			answers.evalName, filepath.ToSlash(ctx.configPath))
	}

	if source != initSourceTraces {
		answers.datasetRef, err = resolveDataset(a.cmd, ctx.cfg, a.flags.dataset)
		if err != nil {
			return initAnswers{}, err
		}
	}

	answers.evaluationLevel, err = resolveEvaluationLevel(a.cmd, a.flags.evaluationLevel)
	if err != nil {
		return initAnswers{}, err
	}

	// Asked, not detected: an eval grades on a set, so there is no "the only
	// one" to settle on, and which criteria define quality is the substantive
	// decision in the configuration.
	answers.evaluators = a.flags.evaluators
	answers.evaluatorsChosen = len(answers.evaluators) > 0
	if len(answers.evaluators) == 0 {
		answers.evaluators, answers.evaluatorsChosen, err = resolveEvaluators(a.cmd, ctx.cfg)
		if err != nil {
			return initAnswers{}, err
		}
	}

	// Only meaningful for a trace-backed eval, and only asked for one: a
	// dataset-backed scaffold that stopped to ask how far back to read would
	// be asking about rows it is not going to read.
	if source == initSourceTraces {
		answers.lookbackHours, err = resolveTraceWindow(
			a.cmd, a.flags.traceDays, a.cmd.Flags().Changed("trace-days"))
		if err != nil {
			return initAnswers{}, err
		}
	}
	return answers, nil
}

// writeLocalContext reports what init settled without asking.
//
// Printed before the questions, because the first thing a reader needs is
// which project this is about: init used to detect the agent, the judge model
// and the configuration file silently, so the only evidence of what it had
// decided was the summary at the end -- after every prompt had already been
// answered against assumptions the reader never saw.
//
// Deliberately without success ticks. Nothing here was validated remotely, and
// a tick beside a name init merely read out of a file claims more than it knows.
func writeLocalContext(out io.Writer, ctx initContext, target, judgeModel string) {
	fmt.Fprint(out, messages.LocalContextHeading())
	fmt.Fprint(out, messages.LocalContextLine("Agent", target))
	fmt.Fprint(out, messages.LocalContextLine("Judge model", judgeModel))
	fmt.Fprint(out, messages.LocalContextLine("Config file",
		messages.ConfigFileState(filepath.ToSlash(ctx.configPath), ctx.configExisted, len(ctx.cfg.Evals))))
}

// What the reader can do with the summary.
const (
	scaffoldAdd = iota
	scaffoldChange
	scaffoldCancel
)

// scaffoldSummary is what the confirmation reports.
type scaffoldSummary struct {
	answers    initAnswers
	configPath string
	// wiring is what the azure.yaml edit will be, so the Files block states
	// the change rather than implying it.
	wiring    string
	maxTraces int
}

// confirmScaffold shows what init is about to write and asks whether to.
//
// Printed before either file is touched. init used to write both and then
// report, so the first time a reader saw which agent, dataset and evaluators
// it had settled on, the settling was already on disk.
func confirmScaffold(cmd *cobra.Command, out io.Writer, s scaffoldSummary) (int, error) {
	if noPrompt(cmd) {
		// The flags are the confirmation. Printing the summary anyway would
		// put a block in front of every CI log for a question nobody answers.
		return scaffoldAdd, nil
	}

	writeScaffoldSummary(out, s)

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return scaffoldCancel, messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.ConfirmScaffoldPrompt(filepath.ToSlash(s.configPath)),
			Choices: []*azdext.SelectChoice{
				{Label: messages.ScaffoldAddChoice(), Value: "add"},
				{Label: messages.ScaffoldChangeChoice(), Value: "change"},
				{Label: messages.ScaffoldCancelChoice(), Value: "cancel"},
			},
			SelectedIndex: preselect(scaffoldAdd),
		},
	})
	if err != nil {
		return scaffoldCancel, messages.ConfirmingScaffold(err)
	}
	// An unanswered prompt is not consent to write two files. Value is
	// optional on the wire, and an unset one arrives as 0 from GetValue --
	// which is exactly the answer that writes them.
	if resp == nil || resp.Value == nil {
		return scaffoldCancel, nil
	}
	switch int(resp.GetValue()) {
	case scaffoldAdd:
		return scaffoldAdd, nil
	case scaffoldChange:
		return scaffoldChange, nil
	default:
		return scaffoldCancel, nil
	}
}

// writeScaffoldSummary prints the block the confirmation is asked about.
func writeScaffoldSummary(out io.Writer, s scaffoldSummary) {
	a := s.answers
	fmt.Fprint(out, messages.ScaffoldSummaryHeading())
	fmt.Fprint(out, messages.ScaffoldSummaryLine("Name", a.evalName))
	fmt.Fprint(out, messages.ScaffoldSummaryLine("Agent", a.target))
	fmt.Fprint(out, messages.ScaffoldSummaryLine("Source", a.source))
	if a.source == initSourceTraces {
		fmt.Fprint(out, messages.ScaffoldSummaryLine(
			"Window", messages.TraceWindowSummary(a.lookbackHours)))
		fmt.Fprint(out, messages.ScaffoldSummaryLine(
			"Maximum traces", fmt.Sprint(s.maxTraces)))
	} else {
		fmt.Fprint(out, messages.ScaffoldSummaryLine("Dataset", a.datasetRef))
	}
	fmt.Fprint(out, messages.ScaffoldSummaryLine("Evaluation level", a.evaluationLevel))
	if a.judgeModel != "" {
		fmt.Fprint(out, messages.ScaffoldSummaryLine("Judge model", a.judgeModel))
	}
	fmt.Fprint(out, messages.ScaffoldSummaryEvaluators(a.evaluators))
	fmt.Fprint(out, messages.ScaffoldSummaryLine(
		"Config file", filepath.ToSlash(s.configPath)))
	fmt.Fprint(out, messages.ScaffoldSummaryFiles(
		filepath.ToSlash(s.configPath), rootConfigName, s.wiring == wiringAdded))
}
