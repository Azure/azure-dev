const winston = require("winston");
const { format } = require("winston");
require("winston-daily-rotate-file");
const fs = require("fs");
const path = require("path");
const os = require("os");

/**
 * Sets up the logger using Winston with daily log rotation
 * @returns {Object} The Winston logger instance
 */
function setupLogger() {
  // Prioritize finding the executable path for log placement
  const possibleLogDirs = [
    // First priority: executable directory (where azd.exe runs from)
    path.dirname(process.execPath),
    // Second priority: module directory (where the JS file is)
    __dirname,
    // Fallbacks if neither of above are writable
    os.homedir(),
    os.tmpdir(),
  ];

  let logDir = null;

  // Try to find a writable directory for log files
  for (const dir of possibleLogDirs) {
    try {
      // Test if directory is writable by creating a temporary file
      const testPath = path.join(dir, ".azd-log-test");
      fs.appendFileSync(testPath, "");
      fs.unlinkSync(testPath);
      logDir = dir;
      break;
    } catch (err) {
      // Continue to the next directory if this one isn't writable
      continue;
    }
  }

  if (!logDir) {
    console.error(
      "WARNING: Could not find writable directory for logs. Logging to console only."
    );
    return winston.createLogger({
      level: "info",
      format: winston.format.combine(
        winston.format.timestamp(),
        winston.format.json()
      ),
      transports: [new winston.transports.Console()],
    });
  }

  // Daily rotate transport for timestamped logs
  const dailyRotateTransport = new winston.transports.DailyRotateFile({
    dirname: logDir,
    filename: "azd-extension-%DATE%.log",
    datePattern: "YYYY-MM-DD",
    maxSize: "20m",
    maxFiles: "14d", // Keep logs for 14 days
    format: format.combine(format.timestamp(), format.json()),
  });

  // Add events for log rotation
  dailyRotateTransport.on("rotate", function (oldFilename, newFilename) {
    console.log(`Log rotated from ${oldFilename} to ${newFilename}`);
  });

  // Create a Winston logger with daily rotation and console transports
  const logger = winston.createLogger({
    level: process.env.LOG_LEVEL || "info",
    format: format.combine(format.timestamp(), format.json()),
    defaultMeta: { service: "azd-extension" },
    transports: [
      dailyRotateTransport,
    ],
    // Catch exceptions and exit gracefully after logging them
    exceptionHandlers: [
      new winston.transports.DailyRotateFile({
        dirname: logDir,
        filename: "azd-extension-exceptions-%DATE%.log",
        datePattern: "YYYY-MM-DD",
        maxFiles: "14d",
      }),
      new winston.transports.Console({
        format: format.combine(format.colorize(), format.simple()),
      }),
    ],
    exitOnError: false,
  });

  // Create human-readable log path for display purposes
  const currentLogPath = path.join(
    logDir,
    `azd-extension-${new Date().toISOString().split("T")[0]}.log`
  );

  // Log initialization message
  logger.info(`Logger initialized with daily rotation`, {
    logDirectory: logDir,
    currentLogFile: currentLogPath,
  });

  return logger;
}

// Create the singleton logger instance
const logger = setupLogger();

module.exports = logger;
