package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/extensions/ai/internal/service"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ext"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newDeploymentCommand() *cobra.Command {
	deploymentCmd := &cobra.Command{
		Use:   "deployment",
		Short: "Commands for managing Azure AI model deployments",
	}

	deploymentListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all deployments",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			azdContext, err := ext.CurrentContext(ctx)
			if err != nil {
				return err
			}

			serviceConfig, err := service.LoadOrPrompt(ctx, azdContext)
			if err != nil {
				return err
			}

			credential, err := azdContext.Credential()
			if err != nil {
				return err
			}

			deployments := []*armcognitiveservices.Deployment{}

			deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(serviceConfig.Subscription, credential, nil)
			if err != nil {
				return err
			}

			deploymentsPager := deploymentsClient.NewListPager(serviceConfig.ResourceGroup, serviceConfig.Service, nil)
			for deploymentsPager.More() {
				pageResponse, err := deploymentsPager.NextPage(ctx)
				if err != nil {
					return err
				}

				deployments = append(deployments, pageResponse.Value...)
			}

			for _, deployment := range deployments {
				fmt.Printf("Name: %s\n", *deployment.Name)
				fmt.Printf("SKU: %s\n", *deployment.SKU.Name)
				fmt.Printf("Model: %s\n", *deployment.Properties.Model.Name)
				fmt.Printf("Version: %s\n", *deployment.Properties.Model.Version)
				fmt.Println()
			}

			return nil
		},
	}

	deploymentCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new model deployment",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			azdContext, err := ext.CurrentContext(ctx)
			if err != nil {
				return err
			}

			serviceConfig, err := service.LoadOrPrompt(ctx, azdContext)
			if err != nil {
				return err
			}

			credential, err := azdContext.Credential()
			if err != nil {
				return err
			}

			clientFactory, err := armcognitiveservices.NewClientFactory(serviceConfig.Subscription, credential, nil)
			if err != nil {
				return err
			}

			accountsClient := clientFactory.NewAccountsClient()
			modelsClient := clientFactory.NewModelsClient()
			deploymentsClient := clientFactory.NewDeploymentsClient()

			loadingSpinner := ux.NewSpinner(&ux.SpinnerConfig{
				Text: "Loading AI models",
			})

			models := []*armcognitiveservices.Model{}

			loadingSpinner.Run(ctx, func(ctx context.Context) error {
				aiService, err := accountsClient.Get(ctx, serviceConfig.ResourceGroup, serviceConfig.Service, nil)
				if err != nil {
					return err
				}

				modelPager := modelsClient.NewListPager(*aiService.Location, nil)
				for modelPager.More() {
					pageResponse, err := modelPager.NextPage(ctx)
					if err != nil {
						return err
					}

					for _, model := range pageResponse.Value {
						if *model.Kind == *aiService.Kind {
							models = append(models, model)
						}
					}
				}

				return nil
			})

			modelChoices := make([]string, len(models))
			for i, model := range models {
				modelChoices[i] = fmt.Sprintf("%s (Version: %s)", *model.Model.Name, *model.Model.Version)
			}

			modelSelect := ux.NewSelect(&ux.SelectConfig{
				Message:        "Select a model",
				Allowed:        modelChoices,
				DisplayCount:   10,
				DisplayNumbers: to.Ptr(true),
			})

			selectedModelIndex, err := modelSelect.Ask()
			if err != nil {
				return err
			}

			selectedModel := models[*selectedModelIndex]

			skuChoices := make([]string, len(selectedModel.Model.SKUs))
			for i, sku := range selectedModel.Model.SKUs {
				skuChoices[i] = *sku.Name
			}

			skuPrompt := ux.NewSelect(&ux.SelectConfig{
				Message: "Select a Deployment Type",
				Allowed: skuChoices,
			})

			selectedSkuIndex, err := skuPrompt.Ask()
			if err != nil {
				return err
			}

			selectedSku := selectedModel.Model.SKUs[*selectedSkuIndex]

			var deploymentName string

			namePrompt := ux.NewPrompt(&ux.PromptConfig{
				Message:      "Enter the name for the deployment",
				DefaultValue: *selectedModel.Model.Name,
			})

			deploymentName, err = namePrompt.Ask()
			if err != nil {
				return err
			}

			deployment := armcognitiveservices.Deployment{
				Name: &deploymentName,
				SKU: &armcognitiveservices.SKU{
					Name:     selectedSku.Name,
					Capacity: selectedSku.Capacity.Default,
				},
				Properties: &armcognitiveservices.DeploymentProperties{
					Model: &armcognitiveservices.DeploymentModel{
						Format:  selectedModel.Model.Format,
						Name:    selectedModel.Model.Name,
						Version: selectedModel.Model.Version,
					},
					RaiPolicyName: to.Ptr("Microsoft.DefaultV2"),
					VersionUpgradeOption: to.Ptr(
						armcognitiveservices.DeploymentModelVersionUpgradeOptionOnceNewDefaultVersionAvailable,
					),
				},
			}

			fmt.Println()

			taskList := ux.NewTaskList(&ux.DefaultTaskListConfig)

			if err := taskList.Run(); err != nil {
				return err
			}

			taskList.AddTask(fmt.Sprintf("Creating deployment %s", deploymentName), func() (ux.TaskState, error) {
				existingDeployment, err := deploymentsClient.Get(
					ctx,
					serviceConfig.ResourceGroup,
					serviceConfig.Service,
					deploymentName,
					nil,
				)
				if err == nil && *existingDeployment.Name == deploymentName {
					return ux.Error, errors.New("deployment with the same name already exists")
				}

				poller, err := deploymentsClient.BeginCreateOrUpdate(
					ctx,
					serviceConfig.ResourceGroup,
					serviceConfig.Service,
					deploymentName,
					deployment,
					nil,
				)
				if err != nil {
					return ux.Error, err
				}

				if _, err := poller.PollUntilDone(ctx, nil); err != nil {
					return ux.Error, err
				}

				return ux.Success, nil
			})

			for {
				if taskList.Completed() {
					if err := taskList.Update(); err != nil {
						return err
					}

					fmt.Println()
					color.Green("Deployment '%s' created successfully", deploymentName)
					break
				}
			}

			return nil
		},
	}

	type deploymentDeleteFlags struct {
		name  string
		force bool
	}

	deleteFlags := &deploymentDeleteFlags{}

	deploymentDeleteCmd := &cobra.Command{
		Use:   "delete <deployment-name>",
		Short: "Delete a model deployment",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			azdContext, err := ext.CurrentContext(ctx)
			if err != nil {
				return err
			}

			serviceConfig, err := service.LoadOrPrompt(ctx, azdContext)
			if err != nil {
				return err
			}

			credential, err := azdContext.Credential()
			if err != nil {
				return err
			}

			clientFactory, err := armcognitiveservices.NewClientFactory(serviceConfig.Subscription, credential, nil)
			if err != nil {
				return err
			}

			deploymentsClient := clientFactory.NewDeploymentsClient()

			if deleteFlags.name == "" {
				deployments := []*armcognitiveservices.Deployment{}

				loadingSpinner := ux.NewSpinner(&ux.SpinnerConfig{
					Text: "Loading AI deployments",
				})

				err := loadingSpinner.Run(ctx, func(ctx context.Context) error {
					deploymentsPager := deploymentsClient.NewListPager(
						serviceConfig.ResourceGroup,
						serviceConfig.Service,
						nil,
					)
					for deploymentsPager.More() {
						pageResponse, err := deploymentsPager.NextPage(ctx)
						if err != nil {
							return err
						}

						deployments = append(deployments, pageResponse.Value...)
					}

					return nil
				})

				if err != nil {
					return err
				}

				if len(deployments) == 0 {
					return fmt.Errorf("no deployments found")
				}

				deploymentChoices := make([]string, len(deployments))
				for i, deployment := range deployments {
					deploymentChoices[i] = fmt.Sprintf(
						"%s (Model: %s, Version: %s)",
						*deployment.Name,
						*deployment.Properties.Model.Name,
						*deployment.Properties.Model.Version,
					)
				}

				deploymentSelect := ux.NewSelect(&ux.SelectConfig{
					Message: "Select a deployment to delete",
					Allowed: deploymentChoices,
				})

				selectedDeploymentIndex, err := deploymentSelect.Ask()
				if err != nil {
					return err
				}

				deleteFlags.name = *deployments[*selectedDeploymentIndex].Name
			}

			_, err = deploymentsClient.Get(ctx, serviceConfig.ResourceGroup, serviceConfig.Service, deleteFlags.name, nil)
			if err != nil {
				return fmt.Errorf("deployment '%s' not found", deleteFlags.name)
			}

			confirmed := to.Ptr(false)

			if !deleteFlags.force {
				confirmPrompt := ux.NewConfirm(&ux.ConfirmConfig{
					Message:      fmt.Sprintf("Are you sure you want to delete the deployment '%s'?", deleteFlags.name),
					DefaultValue: to.Ptr(false),
				})

				confirmed, err = confirmPrompt.Ask()
				if err != nil {
					return err
				}
			}

			fmt.Println()

			taskList := ux.NewTaskList(&ux.DefaultTaskListConfig)

			if err := taskList.Run(); err != nil {
				return err
			}

			taskList.AddTask(fmt.Sprintf("Deleting deployment %s", deleteFlags.name), func() (ux.TaskState, error) {
				if !*confirmed {
					return ux.Skipped, ux.ErrCancelled
				}

				poller, err := deploymentsClient.BeginDelete(
					ctx,
					serviceConfig.ResourceGroup,
					serviceConfig.Service,
					deleteFlags.name,
					nil,
				)
				if err != nil {
					return ux.Error, err
				}

				if _, err := poller.PollUntilDone(ctx, nil); err != nil {
					return ux.Error, err
				}

				return ux.Success, nil
			})

			for {
				if taskList.Completed() {
					if err := taskList.Update(); err != nil {
						return err
					}

					fmt.Println()
					color.Green("Deployment '%s' deleted successfully", deleteFlags.name)
					break
				}

				time.Sleep(1 * time.Second)
				if err := taskList.Update(); err != nil {
					return err
				}
			}

			return nil
		},
	}

	deploymentDeleteCmd.Flags().StringVarP(&deleteFlags.name, "name", "n", "", "Name of the deployment to delete")
	deploymentDeleteCmd.Flags().BoolVarP(&deleteFlags.force, "force", "f", false, "Force deletion without confirmation")

	type deploymentSelectFlags struct {
		deploymentName string
	}

	selectFlags := &deploymentSelectFlags{}

	deploymentSelectCmd := &cobra.Command{
		Use:   "select",
		Short: "Select a model",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			azdContext, err := ext.CurrentContext(ctx)
			if err != nil {
				return err
			}

			// Load AI config
			aiConfig, err := service.LoadOrPrompt(ctx, azdContext)
			if err != nil {
				return err
			}

			// Select model deployment
			if selectFlags.deploymentName == "" {
				selectedDeployment, err := service.PromptModelDeployment(ctx, azdContext)
				if err != nil {
					return err
				}

				aiConfig.Model = *selectedDeployment.Name
			} else {
				aiConfig.Model = selectFlags.deploymentName
			}

			// Update AI Config
			if err := service.Save(ctx, azdContext, aiConfig); err != nil {
				return err
			}

			return nil
		},
	}

	deploymentSelectCmd.Flags().StringVarP(&selectFlags.deploymentName, "name", "n", "", "Model name")

	deploymentCmd.AddCommand(deploymentListCmd)
	deploymentCmd.AddCommand(deploymentCreateCmd)
	deploymentCmd.AddCommand(deploymentDeleteCmd)
	deploymentCmd.AddCommand(deploymentSelectCmd)

	return deploymentCmd
}
