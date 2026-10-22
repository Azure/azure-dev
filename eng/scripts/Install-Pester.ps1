$ErrorActionPreference = 'Stop'

for ($attempt = 1; $attempt -le 3; $attempt++) {
    try {
        Install-Module -Name Pester -RequiredVersion 5.7.1 -Repository PSGallery `
            -Force -Scope CurrentUser -ErrorAction Stop
        break
    } catch {
        if ($attempt -eq 3) {
            throw "Unable to install Pester 5.7.1 from PSGallery after 3 attempts: $($_.Exception.Message)"
        }
        Write-Warning "Pester install attempt $attempt failed: $($_.Exception.Message). Retrying."
        Start-Sleep -Seconds (5 * $attempt)
    }
}
