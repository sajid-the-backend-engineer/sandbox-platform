# Copyright Daytona Platforms Inc.
# SPDX-License-Identifier: Apache-2.0

# frozen_string_literal: true

RSpec.describe Northrays::Config do
  around do |example|
    env_keys = %w[
      NORTHRAYS_API_KEY
      NORTHRAYS_JWT_TOKEN
      NORTHRAYS_API_URL
      NORTHRAYS_TARGET
      NORTHRAYS_ORGANIZATION_ID
      NORTHRAYS_CUSTOM_VAR
    ]
    saved = env_keys.to_h { |key| [key, ENV.delete(key)] }
    example.run
  ensure
    saved.each { |key, value| value ? ENV[key] = value : ENV.delete(key) }
  end

  describe '#initialize' do
    it 'accepts explicit api_key' do
      config = described_class.new(api_key: 'my-key')

      expect(config.api_key).to eq('my-key')
    end

    it 'accepts explicit jwt_token and organization_id' do
      config = described_class.new(jwt_token: 'jwt-tok', organization_id: 'org-42')

      expect(config.jwt_token).to eq('jwt-tok')
      expect(config.organization_id).to eq('org-42')
    end

    it 'defaults api_url to API_URL constant' do
      config = described_class.new(api_key: 'k')

      expect(config.api_url).to eq(described_class::API_URL)
    end

    it 'reads values from ENV when explicit args are missing' do
      ENV['NORTHRAYS_API_KEY'] = 'env-key'
      ENV['NORTHRAYS_API_URL'] = 'https://custom.api'
      ENV['NORTHRAYS_TARGET'] = 'eu'
      ENV['NORTHRAYS_ORGANIZATION_ID'] = 'org-env'

      config = described_class.new

      expect(config.api_key).to eq('env-key')
      expect(config.api_url).to eq('https://custom.api')
      expect(config.target).to eq('eu')
      expect(config.organization_id).to eq('org-env')
    end

    it 'prefers explicit params over ENV' do
      ENV['NORTHRAYS_API_KEY'] = 'env-key'

      config = described_class.new(api_key: 'explicit-key')

      expect(config.api_key).to eq('explicit-key')
    end

    it 'reads .env and .env.local without mutating ENV and prefers .env.local', :real_dotenv do
      Dir.mktmpdir do |dir|
        File.write(File.join(dir, '.env'), <<~ENVFILE)
          NORTHRAYS_API_KEY=env-file-key
          NORTHRAYS_TARGET=us
          NOT_NORTHRAYS=ignored
        ENVFILE
        File.write(File.join(dir, '.env.local'), <<~ENVFILE)
          NORTHRAYS_API_KEY=env-local-key
          NORTHRAYS_API_URL=https://local.api
        ENVFILE

        Dir.chdir(dir) do
          config = described_class.new

          expect(config.api_key).to eq('env-local-key')
          expect(config.target).to eq('us')
          expect(config.api_url).to eq('https://local.api')
          expect(ENV.fetch('NORTHRAYS_API_KEY', nil)).to be_nil
        end
      end
    end

    it 'stores experimental config' do
      config = described_class.new(api_key: 'k', _experimental: { 'otel_enabled' => true })

      expect(config._experimental).to eq({ 'otel_enabled' => true })
    end
  end

  describe '#read_env' do
    it 'returns values for NORTHRAYS_-prefixed variables from ENV' do
      ENV['NORTHRAYS_CUSTOM_VAR'] = 'hello'
      config = described_class.new(api_key: 'k')

      expect(config.read_env('NORTHRAYS_CUSTOM_VAR')).to eq('hello')
    end

    it 'returns nil for unset NORTHRAYS_ variables' do
      config = described_class.new(api_key: 'k')

      expect(config.read_env('NORTHRAYS_NONEXISTENT')).to be_nil
    end

    it 'raises ArgumentError for non-NORTHRAYS_ variable names' do
      config = described_class.new(api_key: 'k')

      expect { config.read_env('OTHER_VAR') }
        .to raise_error(ArgumentError, /Variable must start with 'NORTHRAYS_'/)
    end
  end
end
