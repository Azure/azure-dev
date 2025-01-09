import * as task from 'azure-pipelines-task-lib/task';
import * as cp from 'child_process'
import path from 'path';
import * as fs from 'fs'
import download from 'download';
import decompress from 'decompress';

export async function runMain(): Promise<void> {
    try {
        task.setTaskVariable('hasRunMain', 'true');
        const version = task.getInput('version') || 'latest'

        console.log("using version: " + version)

        // get architecture and os
        const architecture = process.arch
        const os = process.platform

        // map for different platform and arch
        const extensionMap = {
            linux: '.tar.gz',
            darwin: '.zip',
            win32: '.zip'
        }

        const exeMap = {
            linux: '',
            darwin: '',
            win32: '.exe'
        }

        const arm64Map = {
            x64: 'amd64',
            arm64: 'arm64-beta'
        }

        const platformMap = {
            linux: 'linux',
            darwin: 'darwin',
            win32: 'windows'
        }

        // get install url
        const installArray = installUrlForOS(
            os,
            architecture,
            platformMap,
            arm64Map,
            extensionMap,
            exeMap
        )

        const url = `https://azd-release-gfgac2cmf7b8cuay.b02.azurefd.net/azd/standalone/release/${version}/${installArray[0]}`

        console.log(`The Azure Developer CLI collects usage data and sends that usage data to Microsoft in order to help us improve your experience.
You can opt-out of telemetry by setting the AZURE_DEV_COLLECT_TELEMETRY environment variable to 'no' in the shell you use.

Read more about Azure Developer CLI telemetry: https://github.com/Azure/azure-dev#data-collection`)

        console.log(`Installing azd from ${url}`)
        const buffer = await download(url);
        const extractedTo = path.join(task.cwd(), 'azd-install');
        await decompress(buffer, extractedTo);

        let binName
        if (os !== 'win32') {
            binName = 'azd';
        } else {
            binName = 'azd.exe';
        }
        const binPath = path.join(extractedTo, binName);

        fs.symlinkSync(
            path.join(extractedTo, installArray[1]),
            binPath
        )
        task.prependPath(extractedTo)
        console.log(`azd installed to ${extractedTo}`)

        task.exec(binPath, 'version')
    } catch (err: any) {
        task.setResult(task.TaskResult.Failed, err.message);
    }
}

function installUrlForOS(
    os: string,
    architecture: string,
    platformMap: Record<string, string>,
    archMap: Record<string, string>,
    extensionMap: Record<string, string>,
    exeMap: Record<string, string>
): [string, string] {
    const platformPart = `${platformMap[os]}`
    const archPart = `${archMap[architecture]}`

    if (platformPart === `undefined` || archPart === `undefined`) {
        throw new Error(
            `Unsupported platform and architecture: ${architecture} ${os}`
        )
    }

    const installUrl = `azd-${platformPart}-${archPart}${extensionMap[os]}`
    const installUrlForRename = `azd-${platformPart}-${archPart}${exeMap[os]}`

    return [installUrl, installUrlForRename]
}

runMain();
