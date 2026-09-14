-- Configuration checks share test history but do not use a network target.
ALTER TABLE public.test_jobs ALTER COLUMN test_target_id DROP NOT NULL;
ALTER TABLE public.test_jobs ALTER COLUMN test_target_revision DROP NOT NULL;
ALTER TABLE public.test_jobs ADD CONSTRAINT test_target_pair CHECK
 ((test_target_id IS NULL) = (test_target_revision IS NULL));
