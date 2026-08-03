import { mkdtemp, rm } from 'fs/promises'
import * as os from 'os'
import * as path from 'path'
import * as task from 'azure-pipelines-task-lib/task'
import * as toolRunner from 'azure-pipelines-task-lib/toolrunner'

const numericIdentifier = '(?:0|[1-9]\\d*)'
const prereleaseIdentifier = `(?:${numericIdentifier}|\\d*[A-Za-z-][0-9A-Za-z-]*)`
const semanticVersion =
    `${numericIdentifier}\\.${numericIdentifier}\\.${numericIdentifier}` +
    `(?:-${prereleaseIdentifier}(?:\\.${prereleaseIdentifier})*)?` +
    '(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?'
const validVersionPattern = new RegExp(`^(?:latest|daily|${semanticVersion})$`)

function errorMessage(err: unknown): string {
    return err instanceof Error ? err.message : String(err)
}

export async function runMain(): Promise<void> {
    let tempDirectory: string | undefined

    try {
        task.setTaskVariable('hasRunMain', 'true')
        const platform = os.platform()
        const localAppData = process.env.LocalAppData
        const envPath = process.env.PATH
        if (platform === 'win32' && !localAppData) {
            task.setResult(task.TaskResult.Failed, 'LocalAppData environment variable is not defined.')
            return
        }
        if (!envPath) {
            task.setResult(task.TaskResult.Failed, 'PATH environment variable is not defined.')
            return
        }
        const version = task.getInput('version') || 'latest'
        if (version.length > 128 || !validVersionPattern.test(version)) {
            task.setResult(
                task.TaskResult.Failed,
                'Version must be latest, daily, or a semantic version such as 1.2.3.',
            )
            return
        }

        console.log(`Installing azd version ${version} on ${platform}.`)
        tempDirectory = await mkdtemp(path.join(os.tmpdir(), 'setup-azd-'))

        if (platform === 'win32') {
            const installScriptPath = path.join(tempDirectory, 'install-azd.ps1')
            const powershellPath = task.which('powershell', true)
            const download: toolRunner.ToolRunner = task.tool(powershellPath)
            download.arg('-NoLogo')
            download.arg('-NoProfile')
            download.arg('-NonInteractive')
            download.arg('-Command')
            download.arg(
                "$ErrorActionPreference = 'Stop'; " +
                    "Invoke-RestMethod -Uri 'https://aka.ms/install-azd.ps1' -OutFile $env:AZD_INSTALL_SCRIPT",
            )

            const downloadResult = await download.exec({
                env: {
                    ...process.env,
                    AZD_INSTALL_SCRIPT: installScriptPath,
                },
            })
            if (downloadResult !== 0) {
                task.setResult(task.TaskResult.Failed, `Failed to download the azd installer. Exit code: ${downloadResult}`)
                return
            }

            const installer: toolRunner.ToolRunner = task.tool(powershellPath)
            installer.arg('-NoLogo')
            installer.arg('-NoProfile')
            installer.arg('-NonInteractive')
            installer.arg('-File')
            installer.arg(installScriptPath)
            installer.arg('-Version')
            installer.arg(version)
            installer.arg('-Verbose')

            const installResult = await installer.exec()
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
            const curlPath = task.which('curl', true)
            const sudoPath = task.which('sudo', true)
            const installScriptPath = path.join(tempDirectory, 'install-azd.sh')

            const download: toolRunner.ToolRunner = task.tool(curlPath)
            download.arg('-fsSL')
            download.arg('https://aka.ms/install-azd.sh')
            download.arg('-o')
            download.arg(installScriptPath)
            const downloadResult = await download.exec()
            if (downloadResult !== 0) {
                task.setResult(task.TaskResult.Failed, `Failed to download the azd installer. Exit code: ${downloadResult}`)
                return
            }

            const installer: toolRunner.ToolRunner = task.tool(sudoPath)
            installer.arg(bashPath)
            installer.arg(installScriptPath)
            installer.arg('--version')
            installer.arg(version)
            installer.arg('--verbose')

            const installResult = await installer.exec()
            if (installResult !== 0) {
                task.setResult(task.TaskResult.Failed, `Failed to install azd. Exit code: ${installResult}`)
                return
            }
        }

        console.log(`Successfully installed azd version ${version}.`)
    } catch (err: unknown) {
        task.setResult(task.TaskResult.Failed, errorMessage(err))
    } finally {
        if (tempDirectory) {
            try {
                await rm(tempDirectory, { recursive: true, force: true })
            } catch (err: unknown) {
                task.setResult(task.TaskResult.Failed, `Failed to clean up installer files: ${errorMessage(err)}`)
            }
        }
    }
}

runMain()
