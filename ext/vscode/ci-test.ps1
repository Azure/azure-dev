if ($IsLinux) {
    xvfb-run -a npm run ci-test
} else {
    npm run ci-test
}