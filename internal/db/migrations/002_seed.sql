-- +goose Up
-- +goose StatementBegin

-- Flow definition. Kept in a separate migration from the schema so the content of the
-- assessment can be revised without touching structural DDL.

INSERT INTO domains (id, code, name, description) VALUES
    ('11111111-1111-4111-8111-111111111111', 'DEPRESSION', 'Depression & Mood', 'Mood and depressive symptom screening.'),
    ('22222222-2222-4222-8222-222222222222', 'ANXIETY', 'Anxiety & OCD', 'Anxiety, worry, and compulsive symptom screening.'),
    ('33333333-3333-4333-8333-333333333333', 'TRAUMA', 'Trauma & Stress', 'Trauma and acute stress-related symptom screening.');

-- main_reason and risk_check have no domain: the first one routes into a domain, the
-- second is a safety gate asked inside every domain flow.
INSERT INTO questions (id, code, prompt, domain_code, is_entry, active) VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'main_reason', 'What is the main reason you are seeking help today?', NULL, TRUE, TRUE),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'depression_interest', 'During the past two weeks, how often have you felt little interest or pleasure in doing things?', 'DEPRESSION', FALSE, TRUE),
    ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'anxiety_worry', 'During the past two weeks, how often have you felt nervous, anxious, or unable to control worrying?', 'ANXIETY', FALSE, TRUE),
    ('dddddddd-dddd-4ddd-8ddd-dddddddddddd', 'trauma_event', 'Have you experienced an event that still causes you significant emotional distress?', 'TRAUMA', FALSE, TRUE),
    ('eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', 'risk_check', 'Are you currently in immediate danger or at risk of seriously harming yourself or someone else?', NULL, FALSE, TRUE);

-- score_weighted = FALSE on the intake and safety questions: the intake only picks a
-- domain, and the safety gate stops the flow instead of contributing severity. Scoring
-- therefore reflects symptom answers only and stays in the reachable range 0..3.
INSERT INTO question_options (id, question_id, value, label, score, next_question_id, risk_flag, score_weighted, sort_order) VALUES
    ('10000000-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'sad', 'I have been feeling sad or emotionally low', 0, 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', FALSE, FALSE, 1),
    ('10000000-0000-4000-8000-000000000002', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'anxious', 'I feel anxious, worried, or constantly stressed', 0, 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', FALSE, FALSE, 2),
    ('10000000-0000-4000-8000-000000000003', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'trauma', 'I am struggling after a difficult or traumatic experience', 0, 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', FALSE, FALSE, 3),
    ('10000000-0000-4000-8000-000000000004', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'unsure', 'I am not sure', 0, 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', FALSE, FALSE, 4),

    ('10000000-0000-4000-8000-000000000011', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'never', 'Never', 0, NULL, FALSE, TRUE, 1),
    ('10000000-0000-4000-8000-000000000012', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'several_days', 'Several days', 1, NULL, FALSE, TRUE, 2),
    ('10000000-0000-4000-8000-000000000013', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'more_than_half', 'More than half the days', 2, 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', FALSE, TRUE, 3),
    ('10000000-0000-4000-8000-000000000014', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'nearly_every_day', 'Nearly every day', 3, 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', FALSE, TRUE, 4),

    ('10000000-0000-4000-8000-000000000021', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'never', 'Never', 0, NULL, FALSE, TRUE, 1),
    ('10000000-0000-4000-8000-000000000022', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'several_days', 'Several days', 1, NULL, FALSE, TRUE, 2),
    ('10000000-0000-4000-8000-000000000023', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'more_than_half', 'More than half the days', 2, 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', FALSE, TRUE, 3),
    ('10000000-0000-4000-8000-000000000024', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'nearly_every_day', 'Nearly every day', 3, 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', FALSE, TRUE, 4),

    ('10000000-0000-4000-8000-000000000031', 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', 'yes', 'Yes', 2, 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', FALSE, TRUE, 1),
    ('10000000-0000-4000-8000-000000000032', 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', 'no', 'No', 0, NULL, FALSE, TRUE, 2),
    ('10000000-0000-4000-8000-000000000033', 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', 'prefer_not_to_answer', 'Prefer not to answer', 1, NULL, FALSE, TRUE, 3),

    ('10000000-0000-4000-8000-000000000041', 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', 'yes', 'Yes', 0, NULL, TRUE, FALSE, 1),
    ('10000000-0000-4000-8000-000000000042', 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', 'no', 'No', 0, NULL, FALSE, FALSE, 2),
    ('10000000-0000-4000-8000-000000000043', 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', 'not_sure', 'I am not sure', 0, NULL, TRUE, FALSE, 3);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM question_options;
DELETE FROM questions;
DELETE FROM domains;

-- +goose StatementEnd
