#!/bin/bash

# Get the directory of the script
EXTENSION_DIR="$(cd "$(dirname "$0")" && pwd)"

# Change to the script directory
cd "$EXTENSION_DIR" || exit

# Create a safe version of EXTENSION_ID replacing dots with dashes
EXTENSION_ID_SAFE="${EXTENSION_ID//./-}"

# Define output directory
OUTPUT_DIR="${OUTPUT_DIR:-$EXTENSION_DIR/bin}"

# Create output and target directories if they don't exist
mkdir -p "$OUTPUT_DIR"

# Get Git commit hash and build date
COMMIT=$(git rev-parse HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# List of OS and architecture combinations
if [ -n "$EXTENSION_PLATFORM" ]; then
    PLATFORMS=("$EXTENSION_PLATFORM")
else
    PLATFORMS=(
        "windows/amd64"
        "windows/arm64"
        "darwin/amd64"
        "darwin/arm64"
        "linux/amd64"
        "linux/arm64"
    )
fi

# Create a version.py file with version information - this will be embedded in executable
cat > "$EXTENSION_DIR/version.py" << EOF
# This file is auto-generated during build
VERSION = "$EXTENSION_VERSION"
COMMIT = "$COMMIT"
BUILD_DATE = "$BUILD_DATE"
EOF

# Loop through platforms and build
for PLATFORM in "${PLATFORMS[@]}"; do
    OS=$(echo "$PLATFORM" | cut -d'/' -f1)
    ARCH=$(echo "$PLATFORM" | cut -d'/' -f2)

    OUTPUT_NAME="$OUTPUT_DIR/$EXTENSION_ID_SAFE-$OS-$ARCH"

    if [ "$OS" = "windows" ]; then
        OUTPUT_NAME+='.exe'
    fi

    echo "Building for $OS/$ARCH..."

    # Delete the output file if it already exists
    [ -f "$OUTPUT_NAME" ] && rm -f "$OUTPUT_NAME"

    PYTHON_MAIN_FILE="main.py"
    
    OS_TYPE=$(uname -s)
    # Install dependencies based on OS
    if [ "$OS_TYPE" == "Linux" ]; then
        # For Linux, install python3-venv
        echo "Linux detected. Installing dependencies..."
        if ! dpkg -s python3-venv &> /dev/null; then
            echo "python3-venv not found, attempting to install it..."
            if [[ $EUID -ne 0 ]]; then
                echo "Root permission required to install packages. Using sudo..."
                sudo apt-get update
                if [ $? -ne 0 ]; then
                    echo "Failed to update package list."
                    exit 1
                fi
                sudo apt-get install -y python3-venv python3-dev libpython3-dev binutils
                if [ $? -ne 0 ]; then
                    echo "Failed to install required packages."
                    exit 1
                fi
            else
                apt-get update
                if [ $? -ne 0 ]; then
                    echo "Failed to update package list."
                    exit 1
                fi
                apt-get install -y python3-venv python3-dev libpython3-dev binutils
                if [ $? -ne 0 ]; then
                    echo "Failed to install required packages."
                    exit 1
                fi
            fi
        fi
    elif [ "$OS_TYPE" == "Darwin" ]; then
        # For macOS, install python3-venv via Homebrew
        echo "macOS detected. Installing dependencies..."
        if ! command -v brew &> /dev/null; then
            echo "Homebrew not found. Please install Homebrew first: https://brew.sh/"
            exit 1
        fi

        if ! brew list python3 &> /dev/null; then
            echo "Homebrew Python3 not found, installing it..."
            brew install python3
            if [ $? -ne 0 ]; then
                echo "Failed to install Python3 via Homebrew."
                exit 1
            fi
        fi
    else
        echo "Unsupported OS: $OS_TYPE"
        exit 1
    fi

    echo "Creating virtual environment..."
    python3 -m venv .venv
    if [ $? -ne 0 ]; then
        echo "Failed to create virtual environment."
        exit 1
    fi

    source .venv/bin/activate
    if [ $? -ne 0 ]; then
        echo "Failed to activate virtual environment."
        exit 1
    fi

    echo "Installing Python dependencies..."
    pip install -r requirements.txt
    if [ $? -ne 0 ]; then
        echo "Failed to install Python dependencies."
        exit 1
    fi

    PYINSTALLER_NAME="$EXTENSION_ID_SAFE-$OS-$ARCH"
    [ "$OS" = "windows" ] && PYINSTALLER_NAME+='.exe'

    echo "Running PyInstaller for $OS/$ARCH..."
    python3 -m PyInstaller \
        --onefile \
        --add-data "generated_proto:generated_proto" \
        --add-data "version.py:." \
        --distpath "$OUTPUT_DIR" \
        --name "$PYINSTALLER_NAME" \
        "$PYTHON_MAIN_FILE"

    if [ $? -ne 0 ]; then
        echo "An error occurred while building Python extension for $OS/$ARCH"
        exit 1
    fi

    mv "$OUTPUT_DIR/$PYINSTALLER_NAME" "$OUTPUT_NAME"
done

echo "Build completed successfully!"
echo "Binaries are located in the $OUTPUT_DIR directory."
