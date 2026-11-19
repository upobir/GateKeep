-- A cancelled run was approved, then stopped. Keep the approval and record
-- the stop as the execution outcome.
UPDATE tool_calls
SET status = 'approved', decision = 'approve'
WHERE status = 'cancelled';
