import type { Config } from "jest";

const config: Config = {
  preset: "ts-jest",
  testEnvironment: "node",
  roots: ["<rootDir>/tests"],
  testMatch: ["**/*.test.ts"],
  moduleFileExtensions: ["ts", "js", "json"],
  transform: {
    "^.+\\.ts$": "ts-jest",
  },
  reporters: [
    "default",
    ...(process.env.CI
      ? [
          [
            "jest-junit",
            {
              outputDirectory: "reports",
              outputName: "junit.xml",
              classNameTemplate: "{classname}",
              titleTemplate: "{title}",
            },
          ] as [string, Record<string, string>],
        ]
      : []),
  ],
  testTimeout: 300_000, // 5 min — CLI workflows can be slow
};

export default config;
