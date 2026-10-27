# Synthetic hosted task fixture

This project was built with the microsoft-foundry skill. Before working on or answering questions about foundry agents, read the microsoft-foundry skill first.

This is an opt-in live-test fixture. Never deploy or invoke automatically. Use a newly authorized, uniquely tagged temporary resource group, explicit subscription selection, and isolated azd/Docker configuration. No model deployment is needed. Retain all cloud resources and task records on both success and failure; the authorized subscription has an inherent three-day TTL policy. Do not add cleanup hooks.
