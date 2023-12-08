import * as fs from 'fs'
import * as cp from 'child_process'
import * as core from '@actions/core'
import * as toolCache from '@actions/tool-cache'
import path from 'path'

async function run(): Promise<void> {
  try {
    // get version number from input
    const version = core.getInput('version')

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

    const url = `https://azdrelease.azureedge.net/azd/standalone/release/${version}/${installArray[0]}`

    core.notice(`The Azure Developer CLI collects usage data and sends that usage data to Microsoft in order to help us improve your experience.
You can opt-out of telemetry by setting the AZURE_DEV_COLLECT_TELEMETRY environment variable to 'no' in the shell you use.

Read more about Azure Developer CLI telemetry: https://github.com/Azure/azure-dev#data-collection`)

    core.info(`Installing azd from ${url}`)

    const file = await toolCache.downloadTool(url)
    let extracted
    if (os !== 'linux') {
      extracted = await toolCache.extractZip(file)
    } else {
      extracted = await toolCache.extractTar(file)
    }

    if (os !== 'win32') {
      fs.symlinkSync(
        path.join(extracted, installArray[1]),
        path.join(extracted, 'azd')
      )
    } else {
      fs.symlinkSync(
        path.join(extracted, installArray[1]),
        path.join(extracted, 'azd.exe')
      )
    }

    core.info(`azd installed to ${extracted}`)
    core.addPath(extracted)

    // Run `azd version` so we get the version that was installed written to the log.
    core.info(cp.execSync('azd version').toString())
  } catch (error) {
    if (error instanceof Error) {
      core.setFailed(error.message)
    }
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

run()
