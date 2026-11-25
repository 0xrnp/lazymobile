// The app the device tests install on an emulator. It logs when it starts and when it opens a
// link, and crashes on lazymobilee2e://crash so that crash reports can be read back.
// lazymobilee2e://write?value=X saves X as a preference and as a file, and lazymobilee2e://read
// logs what is saved, so that clearing an app's data can be checked.
package dev.lazymobile.e2e;

import android.app.Activity;
import android.content.SharedPreferences;
import android.net.Uri;
import android.os.Bundle;
import android.util.Log;
import android.widget.TextView;

import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;

public class Main extends Activity {
    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        TextView text = new TextView(this);
        text.setText("lazymobile e2e");
        setContentView(text);
        Log.i("LAZYMOBILE_E2E", "launched");
        Uri uri = getIntent().getData();
        if (uri != null) {
            Log.i("LAZYMOBILE_E2E", "opened " + uri);
            SharedPreferences prefs = getSharedPreferences("e2e", MODE_PRIVATE);
            File file = new File(getFilesDir(), "e2e.txt");
            if ("write".equals(uri.getHost())) {
                String value = uri.getQueryParameter("value");
                prefs.edit().putString("e2e_value", value).commit();
                try (FileOutputStream out = new FileOutputStream(file)) {
                    out.write(value.getBytes(StandardCharsets.UTF_8));
                } catch (Exception e) {
                    Log.e("LAZYMOBILE_E2E", "write failed " + e);
                }
                Log.i("LAZYMOBILE_E2E", "wrote " + value);
            }
            if ("read".equals(uri.getHost())) {
                String saved = "none";
                try {
                    if (file.exists()) saved = new String(Files.readAllBytes(file.toPath()), StandardCharsets.UTF_8);
                } catch (Exception e) {
                    Log.e("LAZYMOBILE_E2E", "read failed " + e);
                }
                Log.i("LAZYMOBILE_E2E", "saved preference=" + prefs.getString("e2e_value", "none") + " file=" + saved);
            }
            if ("crash".equals(uri.getHost())) {
                throw new RuntimeException("LAZYMOBILE_E2E crash on purpose: " + uri);
            }
        }
    }
}
