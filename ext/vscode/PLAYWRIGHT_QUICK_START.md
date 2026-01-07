# Quick Start: Playwright E2E Tests

## Installation

```bash
# Install all dependencies including Playwright
npm install

# Install Playwright browsers (Chromium by default)
npx playwright install chromium
```

## Running Tests

```bash
# Run all E2E tests
npm run test:e2e

# Run with interactive UI (recommended for development)
npm run test:e2e:ui

# Run in headed mode (see browser)
npm run test:e2e:headed

# Run in debug mode
npm run test:e2e:debug

# Run specific test file
npx playwright test portal-urls.spec.ts

# Run tests matching a pattern
npx playwright test --grep "portal"
```

## View Results

```bash
# Open HTML report after tests complete
npx playwright show-report
```

## What's Tested

### ✅ Portal URL Construction
- Web Apps, Storage, Cosmos DB, Container Apps
- Resource Group URLs
- URL format validation

### ✅ Template Gallery
- awesome-azd gallery loading
- AI templates page (aka.ms/aiapps)
- GitHub repository links
- Template metadata structure

### ✅ Documentation Links
- Azure Developer CLI docs
- GitHub issues & discussions
- Installation guides
- VS Code marketplace page
- Help & feedback links

## Test Files

```
src/test/e2e/
├── portal-urls.spec.ts          # Azure Portal URL tests
├── template-gallery.spec.ts     # Template discovery tests
├── documentation-links.spec.ts  # Documentation link tests
└── README.md                     # Detailed documentation
```

## Quick Examples

### Run one test
```bash
npx playwright test documentation-links.spec.ts
```

### Run only failed tests
```bash
npx playwright test --last-failed
```

### Generate test code (record interactions)
```bash
npx playwright codegen https://aka.ms/awesome-azd
```

## CI Integration

Tests automatically run with:
- 2 retries on failures (CI only)
- Screenshots on failure
- Video recording on failure
- HTML report generation

## Common Issues

**Browser not installed?**
```bash
npx playwright install
```

**Tests timing out?**
- Check network connectivity
- Verify URLs are accessible
- Increase timeout in config if needed

**Need more browsers?**
Edit `playwright.config.ts` to enable Firefox or Safari.

## Next Steps

- Read full documentation: `src/test/e2e/README.md`
- Check Playwright docs: https://playwright.dev
- Add more tests based on new features!
