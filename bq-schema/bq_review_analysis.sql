-- Copyright 2024 Google LLC
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--      https://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

CREATE OR REPLACE PROCEDURE `play_store_reviews_demo.pre_process_reviews_in_bq`(app_name STRING)
BEGIN

  DECLARE done BOOLEAN DEFAULT FALSE;
  DECLARE current_version STRING;
  DECLARE gemini_result STRING;

  CREATE TEMP TABLE versions AS
  SELECT DISTINCT version
  FROM `play_store_reviews_demo.raw_reviews`
  WHERE last_modified >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 DAY)
    AND star_rating <= 3 AND version != '';

  LOOP
    SET current_version = (SELECT version FROM versions LIMIT 1);

    IF current_version IS NULL THEN
      SET done = TRUE;
    ELSE
      -- Construct and execute dynamic query for the current version
        SET gemini_result = (SELECT ml_generate_text_llm_result FROM ML.GENERATE_TEXT(MODEL `play_store_reviews_demo.gemini_remote_model15`,
          (
            SELECT 
            '''You are a app review summarizer. From the following text that contains user reviews/comments, create a summary with the overall sentiment outlining positives and negatives. Also, for any negative comments (star_rating <= 3), generate tags describing what is wrong.  The output should be a single JSON object with two fields: "summary" and "details". The "summary" field contains the overall summary, and the "details" field is an array of JSON objects, each with "comment_id" and "tags" (all tags per comment_id, comma separated). Format the output strictly as a JSON object.  You cannot return empty for summary because you know how to pick up sensible data from following input text: ''' || combined_comments AS prompt
            FROM (
              SELECT STRING_AGG(TO_JSON_STRING(t), ' ') as combined_comments
              FROM (
                SELECT struct(review_id, star_rating, comments) AS t
                FROM `play_store_reviews_demo.raw_reviews`
                WHERE version = current_version
                  AND last_modified >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 DAY)
              )
            )
          ),
          STRUCT(TRUE AS flatten_json_output, 8192 as max_output_tokens,
                  [STRUCT('HARM_CATEGORY_HATE_SPEECH' AS category, 'BLOCK_NONE' AS threshold),
                  STRUCT('HARM_CATEGORY_DANGEROUS_CONTENT' AS category, 'BLOCK_NONE' AS threshold),
                  STRUCT('HARM_CATEGORY_SEXUALLY_EXPLICIT' AS category, 'BLOCK_NONE' AS threshold),
                  STRUCT('HARM_CATEGORY_HARASSMENT' AS category, 'BLOCK_NONE' AS threshold)] AS safety_settings)
        ));
        
        INSERT INTO `play_store_reviews_demo.reviews_to_process` (app_name, gemini_response, created_at, processed_at, version)
        VALUES (app_name, gemini_result, CURRENT_TIMESTAMP(), NULL, current_version);


      DELETE FROM versions WHERE version = current_version;
    END IF;

    IF done THEN
      LEAVE;
    END IF;
  END LOOP;

END;