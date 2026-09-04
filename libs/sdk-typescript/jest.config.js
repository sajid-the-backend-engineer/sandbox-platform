// Copyright Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: Apache-2.0

/** @type {import('jest').Config} */
module.exports = {
  displayName: 'sdk-typescript',
  preset: '../../jest.preset.js',
  testEnvironment: 'node',
  transform: {
    '^.+\\.[tj]sx?$': [
      'ts-jest',
      {
        tsconfig: '<rootDir>/tsconfig.spec.json',
      },
    ],
  },
  moduleFileExtensions: ['ts', 'tsx', 'js', 'jsx'],
  roots: ['<rootDir>/src'],
  moduleNameMapper: {
    '^@northrays/api-client$': '<rootDir>/../api-client/src/index.ts',
    '^@northrays/toolbox-api-client$': '<rootDir>/../toolbox-api-client/src/index.ts',
    '^@northrays/sdk$': '<rootDir>/src/index.ts',
  },
  coverageDirectory: '../../coverage/libs/sdk-typescript',
}
