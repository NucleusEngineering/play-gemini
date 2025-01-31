CREATE OR REPLACE PROCEDURE `play_store_reviews_demo.pre_process_reviews_in_bq`(package_name STRING)
BEGIN

  DECLARE done BOOLEAN DEFAULT FALSE;
  DECLARE current_version STRING;
  DECLARE gemini_result STRING;

  DECLARE p_limit INT64 DEFAULT 100;
  DECLARE p_offset INT64 DEFAULT 0;
  DECLARE p_page INT64 DEFAULT 0;

  DECLARE total_rows INT64;

  CREATE TEMP TABLE versions AS
  SELECT DISTINCT version, app_name
  FROM `play_store_reviews_demo.raw_reviews`
  WHERE last_modified >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 DAY)
    AND star_rating <= 3 AND version != '' AND app_name = package_name;


  LOOP
    SET current_version = (SELECT version FROM versions WHERE app_name = package_name LIMIT 1);

    IF current_version IS NULL THEN
      SET done = TRUE;
    ELSE
      -- Get the total number of rows matching the criteria
      SET total_rows = (
        SELECT COUNT(*)
        FROM `play_store_reviews_demo.raw_reviews`
        WHERE version = current_version
          AND last_modified >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 DAY)
          AND app_name = package_name
      );

      LOOP
        SET p_offset = p_limit * p_page;

        -- Construct and execute dynamic query for the current version
        EXECUTE IMMEDIATE FORMAT("""
        SELECT ml_generate_text_llm_result FROM ML.GENERATE_TEXT(MODEL `play_store_reviews_demo.gemini_remote_model15`,
          (
            SELECT 
            '''You are a app review summarizer. From the following text that contains user reviews/comments, create a summary with the overall sentiment outlining positives and negatives. Also, for any negative comments (star_rating <= 3), generate tags describing what is wrong.  The output should be a single JSON object with two fields: "summary" and "details". The "summary" field contains the overall summary, and the "details" field is an array of JSON objects, each with "comment_id" and "tags" (all tags per comment_id, comma separated). Format the output strictly as a JSON object.  You cannot return empty for summary because you know how to pick up sensible data from following input text: ''' || combined_comments AS prompt
            FROM (
                SELECT STRING_AGG(TO_JSON_STRING(t), ' ') as combined_comments
                FROM (
                  SELECT struct(review_id, star_rating, comments) AS t
                  FROM `play_store_reviews_demo.raw_reviews`
                  WHERE version = "%s"
                    AND last_modified >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 DAY)
                    AND app_name = "%s"
                    LIMIT %d OFFSET %d
                )
            )
          ),
          STRUCT(TRUE AS flatten_json_output, 8192 as max_output_tokens,
                  [STRUCT('HARM_CATEGORY_HATE_SPEECH' AS category, 'BLOCK_NONE' AS threshold),
                  STRUCT('HARM_CATEGORY_DANGEROUS_CONTENT' AS category, 'BLOCK_NONE' AS threshold),
                  STRUCT('HARM_CATEGORY_SEXUALLY_EXPLICIT' AS category, 'BLOCK_NONE' AS threshold),
                  STRUCT('HARM_CATEGORY_HARASSMENT' AS category, 'BLOCK_NONE' AS threshold)] AS safety_settings)
        );
        """, current_version, package_name, p_limit, p_offset) INTO gemini_result;
        
        INSERT INTO `play_store_reviews_demo.reviews_to_process` (app_name, gemini_response, created_at, processed_at, version)
        VALUES (package_name, gemini_result, CURRENT_TIMESTAMP(), NULL, current_version);

        SET p_page = p_page + 1;

        IF (p_page * p_limit > total_rows) THEN
          LEAVE;
        END IF;

      END LOOP;

      DELETE FROM versions WHERE version = current_version AND app_name = package_name;
    END IF;

    IF done THEN
      LEAVE;
    END IF;
  END LOOP;

END;
