# frozen_string_literal: true

require 'northrays'

northrays = Northrays::Northrays.new

northrays.list(Northrays::ListSandboxesQuery.new(
               limit: 10,
               labels: { 'env' => 'dev' },
               states: [Northrays::SandboxState::STARTED],
               sort: Northrays::SandboxListSortField::CREATED_AT,
               order: Northrays::SandboxListSortDirection::DESC
             )).each do |sandbox|
  puts sandbox.id
end
