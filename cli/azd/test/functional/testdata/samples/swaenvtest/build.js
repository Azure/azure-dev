// Simple build script that outputs environment variables to a file for testing
// This simulates what Vite would do with VITE_* environment variables
import { writeFileSync, mkdirSync, existsSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

// Collect all VITE_* environment variables
const viteEnvVars = {};
for (const [key, value] of Object.entries(process.env)) {
    if (key.startsWith('VITE_')) {
        viteEnvVars[key] = value;
    }
}

// Create dist directory if it doesn't exist
const distDir = join(__dirname, 'dist');
if (!existsSync(distDir)) {
    mkdirSync(distDir, { recursive: true });
}

// Write environment variables to a JSON file for verification
const outputPath = join(distDir, 'env-output.json');
writeFileSync(outputPath, JSON.stringify(viteEnvVars, null, 2));

console.log('Build completed. Environment variables written to dist/env-output.json');
console.log('VITE_* environment variables found:', Object.keys(viteEnvVars));
