-- name: ContractVersion :one
-- The newest read-contract version storage publishes.
select contract_version
from contract.contract_info
order by published_at desc
limit 1;
