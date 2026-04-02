FROM python:3.12-slim

WORKDIR /app

# 의존성만 먼저 복사해서 레이어 캐시 활용
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# 소스 복사
COPY . .

CMD ["python", "bot.py"]
