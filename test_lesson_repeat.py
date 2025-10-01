#!/usr/bin/env python3
"""
Test script for the new lesson repeat endpoint
"""

import requests
import json
import time

# Configuration
BASE_URL = "http://10.66.1.82:8787"
TOKEN = ""  # Add your token if authentication is enabled

def test_lesson_repeat():
    """Test the lesson repeat functionality"""
    
    # Test data
    test_cases = [
        {
            "name": "Default lesson-1 repeat (10 times, 2s black)",
            "data": {}
        },
        {
            "name": "Custom lesson-2 repeat (5 times, 3s black)",
            "data": {
                "lesson": "lesson-2",
                "repeat_count": 5,
                "black_duration": 3,
                "label": "test-lesson-2-repeat"
            }
        },
        {
            "name": "Custom lesson-3 repeat (3 times, 1s black)",
            "data": {
                "lesson": "lesson-3",
                "repeat_count": 3,
                "black_duration": 1,
                "label": "test-lesson-3-repeat"
            }
        }
    ]
    
    headers = {}
    if TOKEN:
        headers["Authorization"] = f"Bearer {TOKEN}"
    
    for test_case in test_cases:
        print(f"\n{'='*60}")
        print(f"Testing: {test_case['name']}")
        print(f"{'='*60}")
        
        # Start lesson repeat
        try:
            response = requests.post(
                f"{BASE_URL}/v1/lesson/repeat",
                json=test_case["data"],
                headers=headers,
                timeout=10
            )
            
            print(f"Status Code: {response.status_code}")
            print(f"Response: {json.dumps(response.json(), indent=2)}")
            
            if response.status_code == 202:
                print("✅ Lesson repeat started successfully!")
                
                # Wait a bit to see it running
                print("Waiting 10 seconds to see it running...")
                time.sleep(10)
                
                # Check status
                status_response = requests.get(f"{BASE_URL}/v1/state", headers=headers)
                if status_response.status_code == 200:
                    status = status_response.json()
                    playout = status.get("playout", {})
                    print(f"Current playout status: {json.dumps(playout, indent=2)}")
                
                # Ask user if they want to stop this test
                user_input = input("\nPress Enter to stop this test and continue, or 'q' to quit: ")
                if user_input.lower() == 'q':
                    break
                
                # Stop the lesson repeat
                stop_response = requests.post(f"{BASE_URL}/v1/lesson/stop", headers=headers)
                print(f"Stop response: {stop_response.status_code} - {stop_response.json()}")
                
            else:
                print("❌ Failed to start lesson repeat")
                
        except requests.exceptions.RequestException as e:
            print(f"❌ Request failed: {e}")
        except Exception as e:
            print(f"❌ Error: {e}")
        
        print(f"\nWaiting 2 seconds before next test...")
        time.sleep(2)

def test_invalid_lesson():
    """Test with invalid lesson parameter"""
    print(f"\n{'='*60}")
    print("Testing invalid lesson parameter")
    print(f"{'='*60}")
    
    headers = {}
    if TOKEN:
        headers["Authorization"] = f"Bearer {TOKEN}"
    
    try:
        response = requests.post(
            f"{BASE_URL}/v1/lesson/repeat",
            json={"lesson": "invalid-lesson"},
            headers=headers,
            timeout=10
        )
        
        print(f"Status Code: {response.status_code}")
        print(f"Response: {json.dumps(response.json(), indent=2)}")
        
        if response.status_code == 400:
            print("✅ Correctly rejected invalid lesson parameter")
        else:
            print("❌ Should have rejected invalid lesson parameter")
            
    except Exception as e:
        print(f"❌ Error: {e}")

if __name__ == "__main__":
    print("Lesson Repeat Endpoint Test")
    print("=" * 60)
    print(f"Testing against: {BASE_URL}")
    print(f"Authentication: {'Enabled' if TOKEN else 'Disabled'}")
    
    # Test valid cases
    test_lesson_repeat()
    
    # Test invalid case
    test_invalid_lesson()
    
    print(f"\n{'='*60}")
    print("Test completed!")
    print(f"{'='*60}")
