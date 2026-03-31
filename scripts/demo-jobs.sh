#!/bin/bash
# GoQueue 데모 작업 생성 스크립트
# 사용법: ./scripts/demo-jobs.sh [간격(초)] [횟수]
# 예시: ./scripts/demo-jobs.sh 3 100

API="http://localhost:8080/api/v1"
API_KEY="change-me-in-production"
INTERVAL=${1:-2}
COUNT=${2:-50}
QUEUES=("default" "email" "notification" "report")
TYPES=("process" "send_email" "push_notify" "generate_report")

echo "GoQueue Demo Job Generator"
echo "간격: ${INTERVAL}초 | 횟수: ${COUNT}회"
echo "---"

for i in $(seq 1 $COUNT); do
  IDX=$((RANDOM % ${#QUEUES[@]}))
  QUEUE=${QUEUES[$IDX]}
  TYPE=${TYPES[$IDX]}
  PRIORITY=$((RANDOM % 10 + 1))

  # 20% 확률로 지연 작업 (5~30초 후 실행)
  DELAY=$((RANDOM % 5))
  RUN_AT=""
  if [ $DELAY -eq 0 ]; then
    FUTURE=$((RANDOM % 25 + 5))
    RUN_AT=$(date -u -v+${FUTURE}S +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "+${FUTURE} seconds" +"%Y-%m-%dT%H:%M:%SZ")
    RUN_AT_JSON=", \"run_at\": \"$RUN_AT\""
    SCHEDULED=" [scheduled +${FUTURE}s]"
  else
    RUN_AT_JSON=""
    SCHEDULED=""
  fi

  PAYLOAD="{\"item_id\": $i, \"priority\": $PRIORITY, \"message\": \"Demo job #$i\"}"

  RESPONSE=$(curl -s -X POST "$API/jobs" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "{\"queue\": \"$QUEUE\", \"type\": \"$TYPE\", \"payload\": $PAYLOAD, \"max_retries\": 3 $RUN_AT_JSON}")

  JOB_ID=$(echo $RESPONSE | grep -o '"id":"[^"]*"' | cut -d'"' -f4 | head -c 8)
  echo "[$i/$COUNT] $QUEUE/$TYPE → $JOB_ID$SCHEDULED"

  sleep $INTERVAL
done

echo "---"
echo "완료! $COUNT개 작업 생성됨"
echo "대시보드: http://localhost:8080/dashboard"
