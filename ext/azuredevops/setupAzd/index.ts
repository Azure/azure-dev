import * as task from 'azure-pipelines-task-lib/task';
import * as cp from 'child_process'

export async function runMain(): Promise<void> {
    try {
        task.setTaskVariable('hasRunMain', 'true');
        const os = process.platform
        const version = task.getInput('version') || 'latest'
        const windowsInstallScript = `powershell -c "$scriptPath = \\"$($env:TEMP)\\install-azd.ps1\\"; Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile $scriptPath; . $scriptPath -Version '${version}' -Verbose:$true; Remove-Item $scriptPath"`
        const linuxOrMacOSInstallScript = `curl -fsSL https://aka.ms/install-azd.sh | sudo bash -s -- --version ${version} --verbose`

        console.log(`Installing azd version ${version} on ${os}.`)

        if (os === 'win32') {
            console.log(cp.execSync(windowsInstallScript).toString())
            
            // Add azd to PATH
            cp.execSync('Write-Host "##vso[task.prependpath]$($env:LocalAppData)\\Programs\\Azure Dev CLI"')
        } else {
            console.log(cp.execSync(linuxOrMacOSInstallScript).toString())
        }

        // Run `azd version` to make sure if azd installation failed, it returns error on windows
        if (os === 'win32') {
            const azdVersion = `"$($env:LocalAppData)\\Programs\\Azure Dev CLI\\azd.exe" version`
            cp.execSync(azdVersion)
        }
    } catch (err: any) {
        task.setResult(task.TaskResult.Failed, err.message);
    }
}

runMain();
