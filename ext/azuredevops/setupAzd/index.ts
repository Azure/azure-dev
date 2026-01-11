import * as task from 'azure-pipelines-task-lib/task'
import * as toolRunner from 'azure-pipelines-task-lib/toolrunner'

export async function runMain(): Promise<void> {
    try {
        task.setTaskVariable('hasRunMain', 'true')
        const os = process.platform
        const localAppData = process.env.LocalAppData
        const envPath = process.env.PATH
        if (os === 'win32' && !localAppData) {
            task.setResult(task.TaskResult.Failed, 'LocalAppData environment variable is not defined.')
            return
        }
        if (!envPath) {
            task.setResult(task.TaskResult.Failed, 'PATH environment variable is not defined.')
            return
        }
        const version = task.getInput('version') || 'latest'

        console.log(`Installing azd version ${version} on ${os}.`)

        if (os === 'win32') {
            const powershellPath = task.which('powershell', true)
            const powershell: toolRunner.ToolRunner = task.tool(powershellPath)
            const installScript = `$scriptPath = "$($env:TEMP)\\install-azd.ps1"; Invoke-RestMethod 'https://aka.ms/install-azd.ps1' -OutFile $scriptPath; . $scriptPath -Version '${version}' -Verbose:$true; Remove-Item $scriptPath`
            powershell.arg('-NoLogo')
            powershell.arg('-NoProfile')
            powershell.arg('-NonInteractive')
            powershell.arg('-Command')
            powershell.arg(installScript)
            
            const installResult = await powershell.exec()
            if (installResult !== 0) {
                task.setResult(task.TaskResult.Failed, `Failed to install azd. Exit code: ${installResult}`)
                return
            }

            // Add azd to PATH
            task.setVariable('PATH', `${envPath};${localAppData}\\Programs\\Azure Dev CLI`)

            // Run `azd version` to make sure installation succeeded
            const azdPath = `${localAppData}\\Programs\\Azure Dev CLI\\azd.exe`
            const azd: toolRunner.ToolRunner = task.tool(azdPath)
            azd.arg('version')
            const versionResult = await azd.exec()
            if (versionResult !== 0) {
                task.setResult(task.TaskResult.Failed, `azd version check failed. Exit code: ${versionResult}`)
                return
            }
        } else {
            const bashPath = task.which('bash', true)
            const bash: toolRunner.ToolRunner = task.tool(bashPath)
            bash.arg('-c')
            bash.arg(`curl -fsSL https://aka.ms/install-azd.sh | sudo bash -s -- --version ${version} --verbose`)
            
            const installResult = await bash.exec()
            if (installResult !== 0) {
                task.setResult(task.TaskResult.Failed, `Failed to install azd. Exit code: ${installResult}`)
                return
            }
        }
        
        console.log(`Successfully installed azd version ${version}.`)
    } catch (err: any) {
        task.setResult(task.TaskResult.Failed, err.message)
    }
}

runMain()
