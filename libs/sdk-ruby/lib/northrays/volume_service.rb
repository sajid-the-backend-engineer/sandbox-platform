# Copyright Daytona Platforms Inc.
# SPDX-License-Identifier: Apache-2.0

# frozen_string_literal: true

module Northrays
  class VolumeService
    include Instrumentation

    # Service for managing Northrays Volumes. Can be used to list, get, create and delete Volumes.
    #
    # @param volumes_api [NorthraysApiClient::VolumesApi]
    # @param otel_state [Northrays::OtelState, nil]
    def initialize(volumes_api, otel_state: nil)
      @volumes_api = volumes_api
      @otel_state = otel_state
    end

    # Create new Volume.
    #
    # @param name [String]
    # @return [Northrays::Volume]
    def create(name) = Volume.new(volumes_api.create_volume(NorthraysApiClient::CreateVolume.new(name:)))

    # Delete a Volume.
    #
    # @param volume [Northrays::Volume]
    # @return [void]
    def delete(volume) = volumes_api.delete_volume(volume.id)

    # Get a Volume by name.
    #
    # @param name [String]
    # @param create [Boolean]
    # @return [Northrays::Volume]
    def get(name, create: false)
      Volume.new(volumes_api.get_volume_by_name(name))
    rescue NorthraysApiClient::ApiError => e
      raise unless create && e.code == 404 && e.message.include?("Volume with name #{name} not found")

      create(name)
    end

    # List all Volumes.
    #
    # @return [Array<Northrays::Volume>]
    def list
      volumes_api.list_volumes.map { |volume| Volume.new(volume) }
    end

    instrument :create, :delete, :get, :list, component: 'VolumeService'

    private

    # @return [NorthraysApiClient::VolumesApi]
    attr_reader :volumes_api

    # @return [Northrays::OtelState, nil]
    attr_reader :otel_state
  end
end
