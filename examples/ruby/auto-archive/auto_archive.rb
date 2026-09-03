# frozen_string_literal: true

require 'northrays'

northrays = Northrays::Northrays.new

# Default interval
first_sandbox = northrays.create
puts "Default auto archive interval: #{first_sandbox.auto_archive_interval}"

# Set interval to 1 hour
first_sandbox.auto_archive_interval = 60
puts "Auto archive interval: #{first_sandbox.auto_archive_interval}"

# Max interval
second_sandbox = northrays.create(Northrays::CreateSandboxFromSnapshotParams.new(auto_archive_interval: 0))
puts "Max auto archive interval: #{second_sandbox.auto_archive_interval}"

# 1 day interval
third_sandbox = northrays.create(Northrays::CreateSandboxFromSnapshotParams.new(auto_archive_interval: 24 * 60))
puts "Auto archive interval: #{third_sandbox.auto_archive_interval}"

northrays.delete(first_sandbox)
northrays.delete(second_sandbox)
northrays.delete(third_sandbox)
